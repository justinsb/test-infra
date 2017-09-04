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
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"
)

const maxResultsCache = 500
const aclPublicRead = "publicRead"

const contentTypeApplicationJson = "application/json"
const contentTypeTextPlain = "text/plain"

type GCSClient struct {
	client *storage.Service
}

var gcsClient *GCSClient
var gcsClientMutex sync.Mutex

func getGCSClient() (*GCSClient, error) {
	gcsClientMutex.Lock()
	defer gcsClientMutex.Unlock()

	if gcsClient == nil {
		scope := storage.DevstorageReadWriteScope

		httpClient, err := google.DefaultClient(context.Background(), scope)
		if err != nil {
			return nil, fmt.Errorf("error building GCS HTTP client: %v", err)
		}

		client, err := storage.New(httpClient)
		if err != nil {
			return nil, fmt.Errorf("error building GCS client: %v", err)
		}

		gcsClient = &GCSClient{
			client: client,
		}
	}
	return gcsClient, nil
}

func isGCSNotFound(err error) bool {
	if err == nil {
		return false
	}
	ae, ok := err.(*googleapi.Error)
	return ok && ae.Code == http.StatusNotFound
}

func isGCSPreconditionFailed(err error) bool {
	if err == nil {
		return false
	}
	ae, ok := err.(*googleapi.Error)
	return ok && ae.Code == http.StatusPreconditionFailed
}

func (g *GCSClient) Read(path string) ([]byte, error) {
	bucket, key, err := g.parsePath(path)
	if err != nil {
		return nil, err
	}

	response, err := g.client.Objects.Get(bucket, key).Download()
	if err != nil {
		if isGCSNotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("error reading %s: %v", path, err)
	}
	if response == nil {
		return nil, fmt.Errorf("no response returned from reading %s", path)
	}
	defer response.Body.Close()

	d, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading %s: %v", path, err)
	}
	return d, nil
}

func (g *GCSClient) Update(path string, predefinedAcl string, contentType googleapi.MediaOption, f func(in []byte) ([]byte, error)) error {
	bucket, key, err := g.parsePath(path)
	if err != nil {
		return err
	}

	for {
		meta, err := g.client.Objects.Get(bucket, key).Do()
		var existing []byte
		var ifGenerationMatch int64
		if err != nil {
			if isGCSNotFound(err) {
				existing = nil
				ifGenerationMatch = 0
			} else {
				return fmt.Errorf("error reading %s for update: %v", path, err)
			}
		} else {
			response, err := g.client.Objects.Get(bucket, key).IfGenerationMatch(meta.Generation).Download()
			if err != nil {
				if isGCSPreconditionFailed(err) {
					continue
				} else {
					return fmt.Errorf("error reading %s for update: %v", path, err)
				}
			}

			if response == nil {
				return fmt.Errorf("no response returned from reading %s", path)
			}
			defer response.Body.Close()

			d, err := ioutil.ReadAll(response.Body)
			if err != nil {
				return fmt.Errorf("error reading %s: %v", path, err)
			}

			ifGenerationMatch = meta.Generation
			existing = d
		}

		updated, err := f(existing)
		if err != nil {
			return err
		}
		// Check for no-op
		if existing == nil {
			if updated == nil {
				return nil
			}
		} else if bytes.Equal(updated, existing) {
			return nil
		}

		hasher := md5.New()
		hasher.Write(updated)
		hasher.Sum(nil)

		obj := &storage.Object{
			Name:    key,
			Md5Hash: base64.StdEncoding.EncodeToString(hasher.Sum(nil)),
		}
		r := bytes.NewReader(updated)
		_, err = g.client.Objects.Insert(bucket, obj).PredefinedAcl(predefinedAcl).IfGenerationMatch(ifGenerationMatch).Media(r, contentType).Do()
		if err != nil {
			if isGCSPreconditionFailed(err) {
				log.Printf("Got version conflict during update; retrying")
				continue
			}
			return fmt.Errorf("error writing %s: %v", path, err)
		}

		return nil
	}
}

func (g *GCSClient) parsePath(path string) (string, string, error) {
	u, err := url.Parse(path)
	if err != nil {
		return "", "", fmt.Errorf("cannot parse URL %q", path)
	}
	bucket := u.Host
	key := strings.TrimPrefix(u.Path, "/")
	return bucket, key, nil
}

func (g *GCSClient) Write(path string, updated []byte, predefinedAcl string, mediaOptions ...googleapi.MediaOption) error {
	bucket, key, err := g.parsePath(path)
	if err != nil {
		return err
	}
	hasher := md5.New()
	hasher.Write(updated)
	hasher.Sum(nil)

	obj := &storage.Object{
		Name:    key,
		Md5Hash: base64.StdEncoding.EncodeToString(hasher.Sum(nil)),
	}
	r := bytes.NewReader(updated)
	if _, err := g.client.Objects.Insert(bucket, obj).PredefinedAcl(predefinedAcl).Media(r, mediaOptions...).Do(); err != nil {
		return fmt.Errorf("error writing %s: %v", path, err)
	}

	return nil
}

// reportFinishedJson creates and writes the finished.json file to the specified path
func reportFinishedJson(report string, buildID string, success bool, metadata map[string]string) error {
	result := "FAILURE"
	if success {
		result = "SUCCESS"
	}

	m := make(map[string]interface{})
	m["timestamp"] = time.Now().Unix()
	m["result"] = result
	m["passed"] = success
	m["metadata"] = metadata

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("error encoding finished.json: %v", err)
	}
	if err := gcsSetFileContents(urlJoin(report, buildID, "finished.json"), b, googleapi.ContentType(contentTypeApplicationJson)); err != nil {
		return fmt.Errorf("error uploading finished.json: %v", err)
	}

	return updateResultCache(report, resultCacheEntry{
		BuildID: buildID,
		Result:  result,
		Passed:  success,
	})
}

// updateResultCache downloads the results cache, appends the new result, and then uploads the new results
func updateResultCache(report string, entry resultCacheEntry) error {
	p := urlJoin(report, "jobResultsCache.json")

	gcsClient, err := getGCSClient()
	if err != nil {
		return err
	}

	return gcsClient.Update(p, aclPublicRead, googleapi.ContentType(contentTypeApplicationJson), func(existing []byte) ([]byte, error) {
		var results []resultCacheEntry

		if len(existing) != 0 {
			err := json.Unmarshal(existing, &results)
			if err != nil {
				log.Printf("ignoring existing result cache %q - malformed", p)
			}
		}

		results = append(results, entry)
		for len(results) > maxResultsCache {
			results = results[1:]
		}

		data, err := json.MarshalIndent(&results, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("error serializing results cache: %v", err)
		}
		return data, nil
	})
}

// reportStartedJson creates and writes the started.json file to the specified path
func reportStartedJson(report string, buildID string) error {
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

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("error encoding started.json: %v", err)
	}
	return gcsSetFileContents(urlJoin(report, buildID, "started.json"), b, googleapi.ContentType(contentTypeApplicationJson))
}

func urlJoin(elems ...string) string {
	for i := range elems {
		elems[i] = strings.TrimSuffix(elems[i], "/")
	}
	return strings.Join(elems, "/")
}

// uploadReportFiles copies files to the report destination
func uploadReportFiles(report string, buildID string, dump string, logpath string) error {
	if logpath != "" {
		if err := gcsUploadFile(urlJoin(report, buildID, "build-log.txt"), logpath); err != nil {
			return fmt.Errorf("error uploading build-log: %v", err)
		}
	}
	if dump != "" {
		if err := gcsUploadArtfiacts(urlJoin(report, buildID, "artifacts"), dump); err != nil {
			return fmt.Errorf("error uploading artifacts: %v", err)
		}
	}

	return gcsSetFileContents(urlJoin(report, "latest-build.txt"), []byte(buildID), googleapi.ContentType(contentTypeTextPlain))
}

func gcsUploadFile(dst, src string) error {
	cmd := exec.Command("gsutil", "-q", "cp", "-a", "public-read", src, dst)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Failed to copy file %q to %q: %v", src, dst, err)
	}
	return nil
}

func gcsSetFileContents(dst string, contents []byte, contentType googleapi.MediaOption) error {
	gcsClient, err := getGCSClient()
	if err != nil {
		return err
	}

	return gcsClient.Write(dst, contents, aclPublicRead, contentType)
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
	// https://github.com/kubernetes/test-infra/blob/master/jenkins/bootstrap.py#L651-L655
	id := os.Getenv("BUILD_ENV")
	if id != "" {
		return id
	}

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

type resultCacheEntry struct {
	Version    string `json:"version"`
	BuildID    string `json:"buildnumber"`
	Result     string `json:"result"`
	JobVersion string `json:"job-version"`
	Passed     bool   `json:"passed"`
}
