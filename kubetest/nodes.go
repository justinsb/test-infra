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
	"log"
	"os/exec"
	"time"
)

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

func isReady(node *node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == "True"
		}
	}
	return false
}

func waitForReadyNodes(desiredCount int, timeout time.Duration) error {
	for stop := time.Now().Add(timeout); time.Now().Before(stop); time.Sleep(30 * time.Second) {
		nodes, err := kubectlGetNodes()
		if err != nil {
			log.Printf("kubectl get nodes failed, sleeping: %v", err)
			continue
		}
		var ready []*node
		for i := range nodes.Items {
			node := &nodes.Items[i]
			if isReady(node) {
				ready = append(ready, node)
			}
		}
		if len(ready) < desiredCount {
			log.Printf("%d (ready nodes) < %d (requested instances), sleeping", len(ready), desiredCount)
			continue
		}
		return nil
	}
	return fmt.Errorf("waiting for ready nodes timed out")
}

type nodeList struct {
	Items []node `json:"items"`
}

type node struct {
	Metadata metadata   `json:"metadata"`
	Status   nodeStatus `json:"status"`
}

type nodeStatus struct {
	Addresses  []nodeAddress   `json:"addresses"`
	Conditions []nodeCondition `json:"conditions"`
}

type nodeAddress struct {
	Address string `json:"address"`
	Type    string `json:"type"`
}

type nodeCondition struct {
	Message string `json:"message"`
	Reason  string `json:"reason"`
	Status  string `json:"status"`
	Type    string `json:"type"`
}

type metadata struct {
	Name string `json:"name"`
}
