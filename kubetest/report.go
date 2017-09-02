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
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// reportFinishedJson creates and writes the finished.json file to the specified path
func reportFinishedJson(report string, build string, tmpdir string, success bool, metadata map[string]string) error {
	m := make(map[string]interface{})
	m["timestamp"] = time.Now().Unix()
	if success {
		m["result"] = "SUCCESS"
	} else {
		m["result"] = "FAILURE"
	}
	m["passed"] = success
	m["metadata"] = metadata

	p := filepath.Join(tmpdir, "finished.json")
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("error writing %q: %v", p, err)
	}
	return gcsUploadFile(urlJoin(report, "finished.json"), p)
}

// reportStartedJson creates and writes the started.json file to the specified path
func reportStartedJson(report string, build string, tmpdir string) error {
	m := make(map[string]interface{})
	m["timestamp"] = time.Now().Unix()

	nodeName := nodeName()
	m["jenkins-node"] = nodeName
	m["node"] = nodeName

	ver := findVersion()
	m["version"] = ver // TODO(fejta): retire
	m["job-version"] = ver

	//if version:
	//data['repo-version'] = version
	//data['version'] = version  # TODO(fejta): retire
	//if repos:
	//pull = repos[repos.main]
	//if ref_has_shas(pull[1]):
	//data['pull'] = pull[1]
	//data['repos'] = repos_dict(repos)

	//	gsutil.upload_json(paths.started, data)
	//	# Upload a link to the build path in the directory
	//	if paths.pr_build_link:
	//	gsutil.upload_text(
	//		paths.pr_build_link,
	//		paths.pr_path,
	//		additional_headers=['-h', 'x-goog-meta-link: %s' % paths.pr_path]
	//)

	p := filepath.Join(tmpdir, "started.json")
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("error writing %q: %v", p, err)
	}
	return gcsUploadFile(urlJoin(report, "started.json"), p)
}

func urlJoin(elems ...string) string {
	for i := range elems {
		elems[i] = strings.TrimSuffix(elems[i], "/")
	}
	return strings.Join(elems, "/")
}

// uploadReportFiles copies files to the report destination
func uploadReportFiles(report string, build string, dump string, logpath string) error {
	if logpath != "" {
		if err := gcsUploadFile(urlJoin(report, build, "build-log.txt"), logpath); err != nil {
			return fmt.Errorf("error uploading build-log: %v", err)
		}
	}
	if dump != "" {
		if err := gcsUploadArtfiacts(urlJoin(report, build, "artifacts"), dump); err != nil {
			return fmt.Errorf("error uploading artifacts: %v", err)
		}
	}

	return gcsSetFileContents(urlJoin(report, "latest-build.txt"), []byte(build))
}

func gcsUploadFile(dst, src string) error {
	cmd := exec.Command("gsutil", "-q", "cp", "-a", "public-read", src, dst)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Failed to copy file %q to %q: %v", src, dst, err)
	}
	return nil
}

func gcsSetFileContents(dst string, contents []byte) error {
	cmd := exec.Command("gsutil", "-q", "cp", "-a", "public-read", "-", dst)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("Failed to get stdin pipe while writing GCS file %q: %v", dst, err)
	}
	if _, err := stdin.Write(contents); err != nil {
		return fmt.Errorf("error writing to stdin pipe while writing %q: %v", dst, err)
	}
	stdin.Close()
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("Failed to write file %q: %v", dst, err)
	}
	return nil
}

func gcsUploadArtfiacts(dst, src string) error {
	cmd := exec.Command("gsutil",
		"-m",                               // Run in parallel
		"-q",                               // Run quietly
		"-o", "GSUtil::use_magicfile=True", // Automatic filetype identification
		"cp",
		"-r",                // copy recusively
		"-c",                // Don't let a single failure stop the batch
		"-z", "log,txt,xml", // Compress these files
		"-a", "public-read", // Public ACL
		src, dst)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Failed to copy file %q to %q: %v", src, dst, err)
	}
	return nil
}

func nodeName() string {
	return "unknown"
}

func getBuildName() string {
	t := time.Now().UTC()

	ts := t.Format("2006_01_02-15_04_05")
	ts = strings.Replace(ts, "_", "", -1)
	nodeName := nodeName()
	hasher := crc32.NewIEEE()
	hasher.Write([]byte(nodeName))
	nodeHash := hasher.Sum32()
	pid := os.Getpid()

	return fmt.Sprintf("%s-%x-%d", ts, nodeHash, pid)
}
