/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

type logDumper struct {
	sshConfig *ssh.ClientConfig

	dir string

	services []string
	files    []string
}

func newLogDumper(sshConfig *ssh.ClientConfig, dir string) (*logDumper, error) {
	d := &logDumper{
		sshConfig: sshConfig,
		dir:       dir,
	}

	d.services = []string{
		"node-problem-detector",
		"kubelet",
		"docker",
		"kops-configuration",
		"protokube",
	}
	d.files = []string{
		"kube-apiserver",
		"kube-scheduler",
		"rescheduler",
		"kube-controller-manager",
		"etcd",
		"etcd-events",
		"glbc",
		"cluster-autoscaler",
		"kube-addon-manager",
		"fluentd",
		"kube-proxy",
		"node-problem-detector",
		"cloud-init-output",
		"startupscript",
		"kern",
		"docker",
	}

	return d, nil
}

func (d *logDumper) DumpAllNodes() error {
	nodes, err := kubectlGetNodes()
	if err != nil {
		return err
	}

	for i := range nodes.Items {
		node := &nodes.Items[i]

		host := ""
		for _, address := range node.Status.Addresses {
			if address.Type == "ExternalIP" {
				host = address.Address
				break
			}
		}

		if host == "" {
			log.Printf("could not find address for %v", node.Metadata.Name)
			continue
		}

		log.Printf("Dumping node %s", node.Metadata.Name)

		d, err := d.connectToNode(node.Metadata.Name, host)
		if err != nil {
			log.Printf("could not connect to %s (%s): %v", node.Metadata.Name, host, err)
			continue
		}

		errors := d.dump()
		for _, e := range errors {
			log.Printf("error dumping %s: %v", node.Metadata.Name, e)
		}

		if err := d.Close(); err != nil {
			log.Printf("error closing connection to %s: %v", node.Metadata.Name, err)
		}
	}

	return nil
}

type logDumperNode struct {
	client *ssh.Client
	dumper *logDumper

	dir string
}

func (d *logDumper) connectToNode(nodeName string, host string) (*logDumperNode, error) {
	client, err := ssh.Dial("tcp", host+":22", d.sshConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to SSH to %q: %v", host, err)
	}
	return &logDumperNode{
		client: client,
		dir:    filepath.Join(d.dir, nodeName),
		dumper: d,
	}, nil
}

func (d *logDumperNode) Close() error {
	return d.client.Close()
}

func (d *logDumperNode) dump() []error {
	var errors []error

	if err := d.shellToFile("sudo journalctl --output=short-precise -k", filepath.Join(d.dir, "kern.log")); err != nil {
		errors = append(errors, err)
	}

	for _, s := range d.dumper.services {
		if err := d.shellToFile("sudo journalctl --output=cat -u "+s+".service", filepath.Join(d.dir, s+".log")); err != nil {
			errors = append(errors, err)
		}
	}

	for _, f := range d.dumper.files {
		// TODO: Capture rotates logs
		if err := d.shellToFile("sudo cat /var/log/"+f+".log", filepath.Join(d.dir, f+".log")); err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}

func (d *logDumperNode) shellToFile(command string, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.Printf("unable to mkdir on %q: %v", filepath.Dir(path), err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("error creating file %q: %v", path, err)
	}
	defer f.Close()

	session, err := d.client.NewSession()
	if err != nil {
		return fmt.Errorf("error creating ssh session: %v", err)
	}
	defer session.Close()

	session.Stdout = f
	session.Stderr = f

	if err := session.Run(command); err != nil {
		return fmt.Errorf("error executing command %q: %v", command, err)
	}

	return nil
}

func kubectlGetNodes() (*nodeList, error) {
	o, err := output(exec.Command("kubectl", "get", "nodes", "-ojson"))
	if err != nil {
		log.Printf("kubectl get nodes failed: %s\n%s", wrapError(err).Error(), string(o))
		return nil, err
	}

	log.Printf("nodes: %s", string(o))

	nodes := &nodeList{}
	if err := json.Unmarshal(o, nodes); err != nil {
		return nil, fmt.Errorf("error parsing kubectl get nodes output: %v", err)
	}

	log.Printf("nodes: %v", nodes)

	return nodes, nil
}

type nodeList struct {
	Items []node `json:"items"`
}

type node struct {
	Metadata metadata   `json:"metadata"`
	Status   nodeStatus `json:"status"`
}

type nodeStatus struct {
	Addresses []nodeAddress `json:"addresses"`
}

type nodeAddress struct {
	Address string `json:"address"`
	Type    string `json:"type"`
}

type metadata struct {
	Name string `json:"name"`
}
