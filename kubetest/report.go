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
	"os"
	"path/filepath"
	"time"
)

// reportFinishedJson creates and writes the finished.json file to the specified path
func reportFinishedJson(path string, success bool, metadata map[string]string) error {
	m := make(map[string]interface{})
	m["timestamp"] = time.Now().Unix()
	if success {
		m["result"] = "SUCCESS"
	} else {
		m["result"] = "FAILURE"
	}
	m["passed"] = success
	m["metadata"] = metadata

	p := filepath.Join(path, "finished.json")
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(m); err != nil {
		return fmt.Errorf("error writing %q: %v", p, err)
	}
	return nil
}

// reportStartedJson creates and writes the started.json file to the specified path
func reportStartedJson(path string) error {
	m := make(map[string]interface{})
	m["timestamp"] = time.Now().Unix()

	nodeName := "unknown"
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

	p := filepath.Join(path, "started.json")
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(m); err != nil {
		return fmt.Errorf("error writing %q: %v", p, err)
	}
	return nil
}
