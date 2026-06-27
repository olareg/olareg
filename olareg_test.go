// Copyright the olareg contributors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package olareg

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/copy"
	"github.com/olareg/olareg/types"
)

func TestServer(t *testing.T) {
	t.Parallel()
	sd, err := genSampleData(t)
	if err != nil {
		t.Errorf("failed to generate sample data: %v", err)
		return
	}
	grace := time.Millisecond * 500
	freq := time.Millisecond * 100
	sleep := (freq * 2) + grace + (time.Millisecond * 100)
	existingRepo := "testrepo"
	existingTag := "v2"
	existingReferrerCountAll := 2
	existingReferrerCountFilter := 1
	existingReferrerAT := "application/example.sbom"
	missingReferrerAT := "application/example.missing"
	corruptRepo := "corrupt"
	corruptTag := "v2"
	warningMsg := "test warning message"
	tempDir := t.TempDir()
	err = copy.Copy(tempDir+"/"+existingRepo, "./testdata/"+existingRepo)
	if err != nil {
		t.Errorf("failed to copy %s to tempDir: %v", existingRepo, err)
		return
	}
	err = copy.Copy(tempDir+"/"+corruptRepo, "./testdata/"+corruptRepo)
	if err != nil {
		t.Errorf("failed to copy %s to tempDir: %v", corruptRepo, err)
		return
	}
	boolT := true
	boolF := false
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ttServer := []struct {
		name             string
		conf             config.Config
		existing         bool
		readOnly         bool
		deleteDisabled   bool
		referrerDisabled bool
		testGC           bool
		testSparse       bool
		testWarn         bool
	}{
		{
			name: "Mem",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency: -1 * time.Second,
					},
				},
				API: config.ConfigAPI{
					DeleteEnabled: &boolT,
					Blob: config.ConfigAPIBlob{
						DeleteEnabled: &boolT,
					},
					Referrer: config.ConfigAPIReferrer{
						Limit: 512 * 1024,
					},
					Warnings: []string{warningMsg},
				},
				Log: logger,
			},
			testGC:   false,
			testWarn: true,
		},
		{
			name: "Mem GC",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency:         freq,
						GracePeriod:       grace,
						RepoUploadMax:     10,
						Untagged:          &boolT,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
				API: config.ConfigAPI{
					DeleteEnabled: &boolT,
					Blob: config.ConfigAPIBlob{
						DeleteEnabled: &boolT,
					},
					Referrer: config.ConfigAPIReferrer{
						Limit: 512 * 1024,
					},
					Warnings: []string{warningMsg},
				},
				Log: logger,
			},
			testGC:   true,
			testWarn: true,
		},
		{
			name: "Mem with Dir",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					RootDir:   "./testdata",
					GC: config.ConfigGC{
						Frequency: -1 * time.Second,
					},
				},
				API: config.ConfigAPI{
					DeleteEnabled: &boolT,
					Blob: config.ConfigAPIBlob{
						DeleteEnabled: &boolT,
					},
					Referrer: config.ConfigAPIReferrer{
						Limit: 512 * 1024,
					},
				},
				Log: logger,
			},
			existing: true,
			testGC:   false,
		},
		{
			name: "Mem with Dir Tweaked",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					RootDir:   "./testdata",
					GC: config.ConfigGC{
						Frequency: -1 * time.Second,
					},
				},
				API: config.ConfigAPI{
					DeleteEnabled: &boolF,
					Manifest: config.ConfigAPIManifest{
						SparseImage: &boolT,
						SparseIndex: &boolT,
					},
					Blob: config.ConfigAPIBlob{
						DeleteEnabled: &boolF,
					},
					Referrer: config.ConfigAPIReferrer{
						Enabled: &boolF,
					},
					Warnings: []string{warningMsg},
				},
				Log: logger,
			},
			existing:         true,
			deleteDisabled:   true,
			referrerDisabled: true,
			testGC:           false,
			testSparse:       true,
			testWarn:         true,
		},
		{
			name: "Dir",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   tempDir,
					GC: config.ConfigGC{
						Frequency: -1 * time.Second,
					},
				},
				API: config.ConfigAPI{
					DeleteEnabled: &boolT,
					Blob: config.ConfigAPIBlob{
						DeleteEnabled: &boolT,
					},
					Referrer: config.ConfigAPIReferrer{
						Limit: 512 * 1024,
					},
				},
				Log: logger,
			},
			existing: true,
			testGC:   false,
		},
		{
			name: "Dir GC",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   tempDir,
					GC: config.ConfigGC{
						Frequency:         freq,
						GracePeriod:       grace,
						RepoUploadMax:     10,
						Untagged:          &boolT,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
				API: config.ConfigAPI{
					DeleteEnabled: &boolT,
					Blob: config.ConfigAPIBlob{
						DeleteEnabled: &boolT,
					},
					Referrer: config.ConfigAPIReferrer{
						Limit: 512 * 1024,
					},
				},
				Log: logger,
			},
			existing: true,
			testGC:   true,
		},
		{
			name: "Dir ReadOnly",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   tempDir,
					ReadOnly:  &boolT,
				},
				API: config.ConfigAPI{
					Referrer: config.ConfigAPIReferrer{
						Limit: 512 * 1024,
					},
					Warnings: []string{warningMsg},
				},
				Log: logger,
			},
			existing: true,
			readOnly: true,
			testWarn: true,
		},
	}
	for _, tcServer := range ttServer {
		t.Run(tcServer.name, func(t *testing.T) {
			t.Parallel()
			// new server
			s := New(tcServer.conf)
			t.Cleanup(func() { _ = s.Close() })
			t.Run("Unknown Method", func(t *testing.T) {
				t.Parallel()
				if _, err := testClientRun(
					t, s, "GET", "/unknown/url", nil,
					testClientRespStatus(http.StatusNotFound),
				); err != nil {
					t.Errorf("unknown URL: %v", err)
				}

				if _, err := testClientRun(
					t, s, "GET", "/v2/unknown/method", nil,
					testClientRespStatus(http.StatusNotFound),
				); err != nil {
					t.Errorf("unknown v2 method: %v", err)
				}
			})
			t.Run("Tag List", func(t *testing.T) {
				if !tcServer.existing {
					return
				}
				t.Parallel()
				resp, err := testAPITagsList(t, s, existingRepo, nil)
				if err != nil {
					return
				}
				tl := types.TagList{}
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read tag body: %v", err)
					return
				}
				err = json.Unmarshal(b, &tl)
				if err != nil {
					t.Errorf("failed to parse tag body: %v", err)
					return
				}
				if len(tl.Tags) < 1 || tl.Name != existingRepo {
					t.Errorf("unexpected response (empty or repo mismatch): %v", tl)
				}
			})
			t.Run("Tag List Missing", func(t *testing.T) {
				t.Parallel()
				resp, err := testClientRun(
					t, s, "GET", "/v2/missing/tags/list", nil,
					testClientRespStatus(http.StatusOK, http.StatusNotFound),
				)
				if err != nil || resp.Code == http.StatusNotFound {
					return
				}
				tl := types.TagList{}
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read tag body: %v", err)
					return
				}
				err = json.Unmarshal(b, &tl)
				if err != nil {
					t.Errorf("failed to parse tag body: %v", err)
					return
				}
				if len(tl.Tags) > 0 || tl.Name != "missing" {
					t.Errorf("unexpected response (empty or repo mismatch): %v", tl)
				}
			})
			t.Run("Path Traversal Attack", func(t *testing.T) {
				if !tcServer.existing {
					return
				}
				t.Parallel()
				resp, err := testClientRun(
					t, s, "GET", "/../../../v2/"+existingRepo+"/tags/list", nil,
					testClientRespStatus(http.StatusOK),
				)
				if err != nil {
					t.Errorf("failed to get tag with path traversal attack")
					return
				}

				tl := types.TagList{}
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read tag body: %v", err)
					return
				}
				err = json.Unmarshal(b, &tl)
				if err != nil {
					t.Errorf("failed to parse tag body: %v", err)
					return
				}
				if len(tl.Tags) < 1 || tl.Name != existingRepo {
					t.Errorf("unexpected response (empty or repo mismatch): %v", tl)
				}
			})

			t.Run("Pull Existing Image and Referrers", func(t *testing.T) {
				if !tcServer.existing || tcServer.referrerDisabled {
					return
				}
				t.Parallel()
				// fetch index
				resp, err := testAPIManifestGet(t, s, existingRepo, existingTag, nil)
				if err != nil {
					return
				}
				digI, err := digest.Parse(resp.Header().Get(types.HeaderDockerDigest))
				if err != nil {
					t.Errorf("unable to parse index digest, %s: %v", resp.Header().Get(types.HeaderDockerDigest), err)
					return
				}
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					return
				}
				i := types.Index{}
				err = json.Unmarshal(b, &i)
				if err != nil {
					t.Errorf("failed to unmarshal index: %v", err)
					return
				}
				if len(i.Manifests) < 1 {
					t.Errorf("failed to find any manifests in index: %v", i)
					return
				}
				// fetch platform specific manifest
				descM := i.Manifests[0]
				resp, err = testAPIManifestGet(t, s, existingRepo, descM.Digest.String(), nil)
				if err != nil {
					return
				}
				b, err = io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					return
				}
				m := types.Manifest{}
				err = json.Unmarshal(b, &m)
				if err != nil {
					t.Errorf("failed to unmarshal manifest: %v", err)
					return
				}
				// fetch config blob
				descC := m.Config
				_, err = testAPIBlobGet(t, s, existingRepo, descC.Digest, nil)
				if err != nil {
					return
				}
				// list referrers
				resp, err = testAPIReferrersList(t, s, existingRepo, digI, "", nil)
				if err != nil {
					return
				}
				b, err = io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					return
				}
				rr := types.Index{}
				err = json.Unmarshal(b, &rr)
				if err != nil {
					t.Errorf("failed to unmarshal referrers response: %v", err)
					return
				}
				if rr.MediaType != types.MediaTypeOCI1ManifestList || rr.SchemaVersion != 2 || len(rr.Manifests) != existingReferrerCountAll {
					t.Errorf("referrers response should be an index, schema 2, with %d manifests: %v", existingReferrerCountAll, rr)
				}
				resp, err = testAPIReferrersList(t, s, existingRepo, digI, existingReferrerAT, nil)
				if err != nil {
					return
				}
				b, err = io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					return
				}
				rr = types.Index{}
				err = json.Unmarshal(b, &rr)
				if err != nil {
					t.Errorf("failed to unmarshal referrers response: %v", err)
					return
				}
				if rr.MediaType != types.MediaTypeOCI1ManifestList || rr.SchemaVersion != 2 || len(rr.Manifests) != existingReferrerCountFilter {
					t.Errorf("referrers response should be an index, schema 2, with %d manifests: %v", existingReferrerCountFilter, rr)
				}
				if len(rr.Manifests) > 0 && rr.Manifests[0].ArtifactType != existingReferrerAT {
					t.Errorf("referrers response only include %s entries: %v", existingReferrerAT, rr)
				}
				resp, err = testAPIReferrersList(t, s, existingRepo, digI, missingReferrerAT, nil)
				if err != nil {
					return
				}
				b, err = io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					return
				}
				rr = types.Index{}
				err = json.Unmarshal(b, &rr)
				if err != nil {
					t.Errorf("failed to unmarshal referrers response: %v", err)
					return
				}
				if rr.MediaType != types.MediaTypeOCI1ManifestList || rr.SchemaVersion != 2 || rr.Manifests == nil || len(rr.Manifests) != 0 {
					t.Errorf("referrers response should be an index, schema 2, with %d manifests: %v", 0, rr)
				}
			})
			t.Run("Pull Existing Image and with Referrers disabled", func(t *testing.T) {
				if !tcServer.existing || !tcServer.referrerDisabled {
					return
				}
				t.Parallel()
				// fetch index
				resp, err := testAPIManifestGet(t, s, existingRepo, existingTag, nil)
				if err != nil {
					return
				}
				digI, err := digest.Parse(resp.Header().Get(types.HeaderDockerDigest))
				if err != nil {
					t.Errorf("unable to parse index digest, %s: %v", resp.Header().Get(types.HeaderDockerDigest), err)
					return
				}
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					return
				}
				i := types.Index{}
				err = json.Unmarshal(b, &i)
				if err != nil {
					t.Errorf("failed to unmarshal index: %v", err)
					return
				}
				if len(i.Manifests) < 1 {
					t.Errorf("failed to find any manifests in index: %v", i)
					return
				}
				// fetch platform specific manifest
				descM := i.Manifests[0]
				resp, err = testAPIManifestGet(t, s, existingRepo, descM.Digest.String(), nil)
				if err != nil {
					return
				}
				b, err = io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					return
				}
				m := types.Manifest{}
				err = json.Unmarshal(b, &m)
				if err != nil {
					t.Errorf("failed to unmarshal manifest: %v", err)
					return
				}
				// fetch config blob
				descC := m.Config
				_, err = testAPIBlobGet(t, s, existingRepo, descC.Digest, nil)
				if err != nil {
					return
				}
				// referrers should be disabled
				_, err = testClientRun(
					t, s, "GET", "/v2/"+existingRepo+"/referrers/"+digI.String(), nil,
					testClientRespStatus(http.StatusMethodNotAllowed),
				)
				if err != nil {
					return
				}
			})
			t.Run("Pull Existing Image and with Delete disabled", func(t *testing.T) {
				if !tcServer.existing || !tcServer.deleteDisabled {
					return
				}
				t.Parallel()
				// fetch index
				resp, err := testAPIManifestGet(t, s, existingRepo, existingTag, nil)
				if err != nil {
					return
				}
				digI, err := digest.Parse(resp.Header().Get(types.HeaderDockerDigest))
				if err != nil {
					t.Errorf("unable to parse index digest, %s: %v", resp.Header().Get(types.HeaderDockerDigest), err)
					return
				}
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					return
				}
				i := types.Index{}
				err = json.Unmarshal(b, &i)
				if err != nil {
					t.Errorf("failed to unmarshal index: %v", err)
					return
				}
				if len(i.Manifests) < 1 {
					t.Errorf("failed to find any manifests in index: %v", i)
					return
				}
				// fetch platform specific manifest
				descM := i.Manifests[0]
				resp, err = testAPIManifestGet(t, s, existingRepo, descM.Digest.String(), nil)
				if err != nil {
					return
				}
				b, err = io.ReadAll(resp.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					return
				}
				m := types.Manifest{}
				err = json.Unmarshal(b, &m)
				if err != nil {
					t.Errorf("failed to unmarshal manifest: %v", err)
					return
				}
				// fetch config blob
				descC := m.Config
				_, err = testAPIBlobGet(t, s, existingRepo, descC.Digest, nil)
				if err != nil {
					return
				}
				// deletes should be disabled
				_, err = testClientRun(
					t, s, "DELETE", "/v2/"+existingRepo+"/manifests/"+existingTag, nil,
					testClientRespStatus(http.StatusMethodNotAllowed),
				)
				if err != nil {
					return
				}
				_, err = testClientRun(
					t, s, "DELETE", "/v2/"+existingRepo+"/manifests/"+digI.String(), nil,
					testClientRespStatus(http.StatusMethodNotAllowed),
				)
				if err != nil {
					return
				}
				_, err = testClientRun(
					t, s, "DELETE", "/v2/"+existingRepo+"/blobs/"+descC.Digest.String(), nil,
					testClientRespStatus(http.StatusMethodNotAllowed),
				)
				if err != nil {
					return
				}
			})
			t.Run("Pull with comma separated header", func(t *testing.T) {
				if !tcServer.existing {
					return
				}
				t.Parallel()
				tcgList := []testClientGen{
					testClientRespStatus(http.StatusOK),
					testClientReqHeader("Accept", strings.Join([]string{types.MediaTypeOCI1Manifest, types.MediaTypeOCI1ManifestList}, ", ")),
					testClientReqHeader("Accept", strings.Join([]string{types.MediaTypeDocker2Manifest, types.MediaTypeDocker2ManifestList}, ", ")),
				}
				_, err := testClientRun(t, s, "GET", "/v2/"+existingRepo+"/manifests/"+existingTag, nil, tcgList...)
				if err != nil {
					t.Errorf("failed to get manifest: %v", err)
				}
			})
			t.Run("AMD64", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC {
					return
				}
				se := *sd["image-amd64"]
				t.Parallel()
				if err := testSampleEntryPush(t, s, se, "push-amd64", "v1"); err != nil {
					return
				}
				_ = testSampleEntryPull(t, s, se, "push-amd64", "v1")
			})
			t.Run("Index", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC {
					return
				}
				se := *sd["index"]
				t.Parallel()
				if err := testSampleEntryPush(t, s, se, "index", "index"); err != nil {
					return
				}
				_ = testSampleEntryPull(t, s, se, "index", "index")
			})
			t.Run("Foreign layers", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC {
					return
				}
				se := *sd["image-foreign"]
				t.Parallel()
				if err := testSampleEntryPush(t, s, se, "image-foreign", "image-foreign"); err != nil {
					return
				}
				_ = testSampleEntryPull(t, s, se, "image-foreign", "image-foreign")
			})
			t.Run("Sparse Image and Index", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC {
					return
				}
				repo := "sparse"
				for _, name := range []string{"index", "image-amd64"} {
					dig := sd[name].manifestList[len(sd[name].manifestList)-1]
					body := sd[name].manifest[dig]
					var tcgList []testClientGen
					// expected result depends on whether registry is configured for sparse manifest support
					if tcServer.testSparse {
						tcgList = append(
							tcgList,
							testClientRespStatus(http.StatusCreated),
							testClientRespHeader("Location", ""),
							testClientRespHeader(types.HeaderDockerDigest, dig.String()),
						)
					} else {
						tcgList = append(
							tcgList,
							testClientRespStatus(http.StatusBadRequest),
						)
					}
					if mt := detectMediaType(body); mt != "" {
						tcgList = append(tcgList, testClientReqHeader("Content-Type", mt))
					}
					_, err := testClientRun(t, s, "PUT", "/v2/"+repo+"/manifests/"+dig.String(), body, tcgList...)
					if err != nil {
						if !errors.Is(err, errValidationFailed) {
							t.Errorf("sparse manifest put: %v", err)
						}
					}
				}
			})
			t.Run("Read Only Push", func(t *testing.T) {
				if !tcServer.readOnly {
					return
				}
				se := *sd["index"]
				t.Parallel()
				// blob post should be forbidden
				u, err := url.Parse("/v2/read-write/blobs/uploads/")
				if err != nil {
					t.Errorf("failed to parse blob post url: %v", err)
					return
				}
				_, err = testClientRun(t, s, "POST", u.String(), nil,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientRespStatus(http.StatusForbidden))
				if err != nil && !errors.Is(err, errValidationFailed) {
					t.Errorf("failed to test blob post: %v", err)
				}
				// manifest post should be forbidden
				tcgList := []testClientGen{
					testClientRespStatus(http.StatusForbidden),
				}
				if mt := detectMediaType(se.manifest[se.manifestList[0]]); mt != "" {
					tcgList = append(tcgList, testClientReqHeader("Content-Type", mt))
				}
				_, err = testClientRun(t, s, "PUT", "/v2/read-write/manifests/latest", se.manifest[se.manifestList[0]], tcgList...)
				if err != nil && !errors.Is(err, errValidationFailed) {
					t.Errorf("failed to send manifest put: %v", err)
				}
			})
			t.Run("Garbage Collect", func(t *testing.T) {
				if !tcServer.testGC || tcServer.readOnly {
					return
				}
				seAMD := *sd["image-amd64"]
				seARM := *sd["image-arm64"]
				t.Parallel()
				// push two images with three tags
				if err := testSampleEntryPush(t, s, seAMD, "gc", "amd64"); err != nil {
					return
				}
				if err := testSampleEntryPush(t, s, seAMD, "gc", "amd64-copy"); err != nil {
					return
				}
				if err := testSampleEntryPush(t, s, seARM, "gc", "arm64"); err != nil {
					return
				}
				// delete one manifest by digest
				if _, err := testAPIManifestRm(t, s, "gc", seARM.manifestList[0].String()); err != nil {
					t.Fatalf("failed to remove manifest: %v", err)
				}
				// delete the other manifest by tag
				if _, err := testAPIManifestRm(t, s, "gc", "amd64-copy"); err != nil {
					t.Fatalf("failed to remove manifest: %v", err)
				}
				// delay for GC
				for retry := range 10 {
					time.Sleep(sleep + (time.Millisecond * 200 * time.Duration(retry)))
					remaining := false
					for dig := range seARM.blob {
						if _, ok := seAMD.blob[dig]; ok {
							continue // skip dup blobs
						}
						resp, err := testClientRun(t, s, "HEAD", "/v2/gc/blobs/"+dig.String(), nil)
						if err != nil {
							t.Errorf("failed to run head request: %v", err)
						}
						if resp.Result().StatusCode == http.StatusOK {
							remaining = true
							t.Logf("waiting for %s to be cleaned", dig.String())
							break
						}
					}
					if !remaining {
						t.Logf("All deleted after %d tries", retry)
						break
					}
				}
				// verify get
				if err := testSampleEntryPull(t, s, seAMD, "gc", "amd64"); err != nil {
					t.Errorf("failed to pull entry after recreating server: %v", err)
				}
				// verify GC
				if _, err := testClientRun(t, s, "GET", "/v2/gc/manifest/amd64-copy", nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientRespStatus(http.StatusNotFound)); err != nil {
					t.Errorf("pulled image after it should have been deleted and GC'd: %v", err)
				} else {
					t.Log("amd64-copy was garbage collected")
				}
				if _, err := testClientRun(t, s, "GET", "/v2/gc/manifest/arm64", nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientRespStatus(http.StatusNotFound)); err != nil {
					t.Errorf("pulled image after it should have been deleted and GC'd: %v", err)
				} else {
					t.Log("arm64 was garbage collected")
				}
				for dig := range seARM.blob {
					if _, ok := seAMD.blob[dig]; ok {
						continue // skip dup blobs
					}
					if _, err := testClientRun(t, s, "GET", "/v2/gc/blobs/"+dig.String(), nil,
						testClientRespStatus(http.StatusNotFound)); err != nil {
						t.Errorf("did not receive a not-found error on a GC blob: %v", err)
					} else {
						t.Logf("blog GC verified: %s", dig.String())
					}
				}
				for dig := range seAMD.blob {
					if _, err := testClientRun(t, s, "HEAD", "/v2/gc/blobs/"+dig.String(), nil,
						testClientRespStatus(http.StatusOK)); err != nil {
						t.Errorf("GC blob of image that should have been preserved: %v", err)
					} else {
						t.Logf("blog retention verified: %s", dig.String())
					}
				}
			})
			t.Run("Blob Push Monolithic", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC {
					return
				}
				t.Parallel()
				repo := "monolithic"
				// send good blob
				exBlob := []byte(`example monolithic content`)
				exDigGood, err := digest.Canonical.FromBytes(exBlob)
				if err != nil {
					t.Fatalf("failed to generate digest: %v", err)
				}
				u, err := url.Parse("/v2/" + repo + "/blobs/uploads/")
				if err != nil {
					t.Fatalf("failed to parse blob post url: %v", err)
				}
				q := u.Query()
				q.Add("digest", exDigGood.String())
				u.RawQuery = q.Encode()
				_, err = testClientRun(t, s, "POST", u.String(), exBlob,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientReqHeader("Content-Length", fmt.Sprintf("%d", len(exBlob))),
					testClientRespStatus(http.StatusCreated),
					testClientRespHeader(types.HeaderDockerDigest, exDigGood.String()),
					testClientRespHeader("Location", ""))
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send blob post: %v", err)
					}
					return
				}
				_, err = testAPIBlobGet(t, s, repo, exDigGood, exBlob)
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to get pushed blob: %v", err)
					}
					return
				}
				// send a bad blob
				exDigBad, err := digest.Canonical.FromString("bad blob digest")
				if err != nil {
					t.Fatalf("failed to generate digest: %v", err)
				}
				q.Set("digest", exDigBad.String())
				u.RawQuery = q.Encode()
				_, err = testClientRun(t, s, "POST", u.String(), exBlob,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientReqHeader("Content-Length", fmt.Sprintf("%d", len(exBlob))),
					testClientRespStatus(http.StatusBadRequest))
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send blob post: %v", err)
					}
					return
				}
			})
			t.Run("Blob Push Cancel", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC {
					return
				}
				t.Parallel()
				repo := "cancel"
				exBlob := []byte(`example cancel content`)
				u, err := url.Parse("/v2/" + repo + "/blobs/uploads/")
				if err != nil {
					t.Fatalf("failed to parse blob post url: %v", err)
				}
				resp, err := testClientRun(t, s, "POST", u.String(), nil,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientRespStatus(http.StatusAccepted),
					testClientRespHeader("Location", ""))
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send blob post: %v", err)
					}
					return
				}
				loc := resp.Header().Get("Location")
				if loc == "" {
					t.Fatalf("location header missing on blob POST")
				}
				uLoc, err := url.Parse(loc)
				if err != nil {
					t.Fatalf("failed to parse location header URL fragment %s: %v", loc, err)
				}
				u = u.ResolveReference(uLoc)
				resp, err = testClientRun(t, s, "PATCH", u.String(), exBlob,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientReqHeader("Content-Length", fmt.Sprintf("%d", len(exBlob))),
					testClientRespStatus(http.StatusAccepted))
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send blob patch: %v", err)
					}
					return
				}
				loc = resp.Header().Get("Location")
				if loc == "" {
					t.Fatalf("location header missing on blob POST")
				}
				uLoc, err = url.Parse(loc)
				if err != nil {
					t.Fatalf("failed to parse location header URL fragment %s: %v", loc, err)
				}
				u = u.ResolveReference(uLoc)
				_, err = testClientRun(t, s, "DELETE", u.String(), nil,
					testClientRespStatus(http.StatusNoContent))
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send blob delete: %v", err)
					}
					return
				}
			})
			t.Run("Corrupt Repo", func(t *testing.T) {
				if !tcServer.existing || tcServer.referrerDisabled {
					return
				}
				t.Parallel()
				// get the digest for the existing v2 manifest
				resp, err := testAPIManifestHead(t, s, existingRepo, existingTag, nil)
				if err != nil {
					t.Fatalf("failed to get manifest for existing tag: %v", err)
				}
				digStr := resp.Header().Get(types.HeaderDockerDigest)
				if digStr == "" {
					t.Fatalf("failed to get digest for v2 tag")
				}
				dig, err := digest.Parse(digStr)
				if err != nil {
					t.Fatalf("failed to parse digest %s: %v", digStr, err)
				}
				mt := resp.Header().Get("Content-Type")
				if mt == "" {
					t.Fatalf("media type missing for v2 tag")
				}
				// get the v2 manifest, verify it is 404
				_, err = testClientRun(
					t, s, "GET", "/v2/"+corruptRepo+"/manifests/"+corruptTag, nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientRespStatus(http.StatusNotFound),
				)
				if err != nil {
					t.Errorf("corrupt manifest did not 404 by tag")
				}
				// get the v2 manifest by digest, verify it is 404
				_, err = testClientRun(
					t, s, "GET", "/v2/"+corruptRepo+"/manifests/"+dig.String(), nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientRespStatus(http.StatusNotFound),
				)
				if err != nil {
					t.Errorf("corrupt manifest did not 404 by digest")
				}

				if tcServer.readOnly {
					return
				}
				// push a referrer to the corrupt repo with the v2 subject
				emptyBlobBytes := []byte("{}")
				emptyBlobDig, err := digest.Canonical.FromBytes(emptyBlobBytes)
				if err != nil {
					t.Fatalf("failed to generate digest: %v", err)
				}
				_, err = testAPIBlobPostPut(t, s, corruptRepo, emptyBlobDig, emptyBlobBytes)
				if err != nil {
					t.Fatalf("failed to push empty blob: %v", err)
				}
				referrerMan := types.Manifest{
					SchemaVersion: 2,
					MediaType:     types.MediaTypeOCI1Manifest,
					ArtifactType:  "application/example.test",
					Config: types.Descriptor{
						MediaType: types.MediaTypeOCI1Empty,
						Digest:    emptyBlobDig,
						Size:      int64(len(emptyBlobBytes)),
					},
					Layers: []types.Descriptor{
						{
							MediaType: types.MediaTypeOCI1Empty,
							Digest:    emptyBlobDig,
							Size:      int64(len(emptyBlobBytes)),
						},
					},
					Subject: &types.Descriptor{
						MediaType: mt,
						Digest:    dig,
						Size:      resp.Result().ContentLength,
					},
				}
				referrerBytes, err := json.Marshal(referrerMan)
				if err != nil {
					t.Fatalf("failed to marshal referrer: %v", err)
				}
				referrerDig, err := digest.Canonical.FromBytes(referrerBytes)
				if err != nil {
					t.Fatalf("failed to generate digest: %v", err)
				}
				_, err = testAPIManifestPut(t, s, corruptRepo, referrerDig.String(), referrerBytes)
				if err != nil {
					t.Fatalf("failed to push referrer: %v", err)
				}
				// verify referrer response is a single entry
				refResp, err := testAPIReferrersList(t, s, corruptRepo, dig, "", nil)
				if err != nil {
					t.Fatalf("failed to get referrers: %v", err)
				}
				refRespIndex := types.Index{}
				err = json.Unmarshal(refResp.Body.Bytes(), &refRespIndex)
				if err != nil {
					t.Fatalf("failed to unmarshal referrer response: %v", err)
				}
				if len(refRespIndex.Manifests) != 1 {
					t.Fatalf("unexpected number of referrers, expected 1, received %d", len(refRespIndex.Manifests))
				}
				if refRespIndex.Manifests[0].Digest != referrerDig {
					t.Errorf("unexpected digest for referrer entry, expected %s, received %s", referrerDig.String(), refRespIndex.Manifests[0].Digest.String())
				}
			})
			t.Run("Referrers", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC || tcServer.referrerDisabled || tcServer.deleteDisabled {
					return
				}
				se := *sd["index"]
				t.Parallel()
				count := 25
				bigEntry := 3
				refIdx := types.Index{}
				// push sample image
				if err := testSampleEntryPush(t, s, se, "referrer", "index"); err != nil {
					return
				}
				subDig := se.manifestList[len(se.manifestList)-1]
				subLen := int64(len(se.manifest[subDig]))
				// verify referrers response is empty before pushing any artifacts
				resp, err := testAPIReferrersList(t, s, "referrer", subDig, "", nil)
				if err != nil {
					return
				}
				err = json.NewDecoder(resp.Result().Body).Decode(&refIdx)
				if err != nil {
					t.Fatalf("failed to decode referrers body: %v", err)
				}
				if refIdx.SchemaVersion != 2 || refIdx.MediaType != types.MediaTypeOCI1ManifestList || refIdx.Manifests == nil || len(refIdx.Manifests) > 0 {
					t.Errorf("empty referrer list is not a valid index, received %v", refIdx)
				}
				// generate referrer manifest with annotations, push blob
				emptyBlob := []byte(`{}`)
				emptyDig, err := digest.Canonical.FromBytes(emptyBlob)
				if err != nil {
					t.Fatalf("failed to generate digest: %v", err)
				}
				if _, err := testAPIBlobPostPut(t, s, "referrer", emptyDig, emptyBlob); err != nil {
					return
				}
				exBlob := []byte(`example artifact content`)
				exDig, err := digest.Canonical.FromBytes(exBlob)
				if err != nil {
					t.Fatalf("failed to generate digest: %v", err)
				}
				if _, err := testAPIBlobPostPut(t, s, "referrer", exDig, exBlob); err != nil {
					return
				}
				artMan := types.Manifest{
					SchemaVersion: 2,
					MediaType:     types.MediaTypeOCI1Manifest,
					ArtifactType:  "application/example",
					Config: types.Descriptor{
						MediaType: types.MediaTypeOCI1Empty,
						Digest:    emptyDig,
						Size:      int64(len(emptyBlob)),
					},
					Layers: []types.Descriptor{
						{
							MediaType: "application/example",
							Digest:    exDig,
							Size:      int64(len(exBlob)),
						},
					},
					Annotations: map[string]string{},
					Subject: &types.Descriptor{
						MediaType: types.MediaTypeOCI1ManifestList,
						Digest:    subDig,
						Size:      subLen,
					},
				}
				// pad the manifests to force pagination
				for _, c := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
					artMan.Annotations["filler"+c] = strings.Repeat(c, 5000)
				}
				// in loop, update one annotation, and push
				artDigList := make([]digest.Digest, 0, count+1)
				for i := range count {
					artMan.Annotations["count"] = fmt.Sprintf("%d", i)
					if i == bigEntry {
						// push a jumbo artifact that exceeds the size limit
						artMan.Annotations["tooBig"] = strings.Repeat("A", 512*1024)
					}
					artBytes, err := json.Marshal(artMan)
					if err != nil {
						t.Fatalf("failed to marshal artifact")
					}
					artDig, err := digest.Canonical.FromBytes(artBytes)
					if err != nil {
						t.Fatalf("failed to generate digest: %v", err)
					}
					if _, err := testAPIManifestPut(t, s, "referrer", artDig.String(), artBytes); err != nil {
						return
					}
					artDigList = append(artDigList, artDig)
					if i == bigEntry {
						delete(artMan.Annotations, "tooBig")
					}
				}
				// query for referrer, verify link header
				found := map[string]bool{}
				pageCount := 1
				tcgList := []testClientGen{
					testClientRespStatus(http.StatusOK),
					testClientRespHeader("Content-Type", types.MediaTypeOCI1ManifestList),
				}
				path := "/v2/referrer/referrers/" + subDig.String()
				resp, err = testClientRun(t, s, "GET", path, nil, tcgList...)
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send referrers list: %v", err)
					}
					return
				}
				nextLink, err := parseLinkHeader(resp.Result().Header.Get("link"))
				if err != nil {
					t.Fatalf("%v", err)
				}
				if nextLink == "" {
					t.Errorf("referrers result did not trigger pagination, size = %d", resp.Result().ContentLength)
				}
				err = json.NewDecoder(resp.Result().Body).Decode(&refIdx)
				if err != nil {
					t.Fatalf("failed to decode referrers body: %v", err)
				}
				for _, d := range refIdx.Manifests {
					if d.Annotations == nil {
						continue
					}
					if c, ok := d.Annotations["count"]; ok {
						found[c] = true
					}
				}
				// add another referrer
				artMan.Annotations["count"] = fmt.Sprintf("%d", count)
				artBytes, err := json.Marshal(artMan)
				if err != nil {
					t.Fatalf("failed to marshal artifact")
				}
				artDig, err := digest.Canonical.FromBytes(artBytes)
				if err != nil {
					t.Fatalf("failed to generate digest: %v", err)
				}
				if _, err := testAPIManifestPut(t, s, "referrer", artDig.String(), artBytes); err != nil {
					return
				}
				artDigList = append(artDigList, artDig)
				// get paginated responses using the link header
				for nextLink != "" {
					pageCount++
					resp, err = testClientRun(t, s, "GET", nextLink, nil, tcgList...)
					if err != nil {
						if !errors.Is(err, errValidationFailed) {
							t.Errorf("failed to send referrers list: %v", err)
						}
						return
					}
					nextLink, err = parseLinkHeader(resp.Result().Header.Get("link"))
					if err != nil {
						t.Fatalf("%v", err)
					}
					err = json.NewDecoder(resp.Result().Body).Decode(&refIdx)
					if err != nil {
						t.Fatalf("failed to decode referrers body: %v", err)
					}
					for _, d := range refIdx.Manifests {
						if d.Annotations == nil {
							continue
						}
						if c, ok := d.Annotations["count"]; ok {
							found[c] = true
						}
					}
				}
				t.Logf("received %d pages of referrers", pageCount)
				expected := count
				if count > bigEntry {
					expected-- // do not expect large entry
				}
				if len(found) != expected {
					t.Errorf("length of descriptor list, expected %d, received %d", expected, len(found))
				}
				// verify full list contains all count values (make a slice of bool, set to true as each one is found, verify full list is true)
				for i := range count {
					if i == bigEntry {
						if found[fmt.Sprintf("%d", i)] {
							t.Errorf("response for big entry found for %d", i)
						}
					} else {
						if !found[fmt.Sprintf("%d", i)] {
							t.Errorf("response not found for %d", i)
						}
					}
				}
				if found[fmt.Sprintf("%d", count)] {
					t.Errorf("referrer pushed after first page returned should not have been included")
				}
				// delete each referrer manifest
				for _, artDig := range artDigList {
					if _, err := testAPIManifestRm(t, s, "referrer", artDig.String()); err != nil {
						return
					}
				}
				// verify referrer list is empty
				resp, err = testAPIReferrersList(t, s, "referrer", subDig, "", nil)
				if err != nil {
					return
				}
				err = json.NewDecoder(resp.Result().Body).Decode(&refIdx)
				if err != nil {
					t.Fatalf("failed to decode referrers body: %v", err)
				}
				if refIdx.SchemaVersion != 2 || refIdx.MediaType != types.MediaTypeOCI1ManifestList || refIdx.Manifests == nil {
					t.Errorf("referrers response is not a valid index")
				}
				if len(refIdx.Manifests) > 0 {
					t.Errorf("referrer list was not empty after delete, received %v", refIdx)
				}
				// query again to verify cache works
				_, err = testAPIReferrersList(t, s, "referrer", subDig, "", nil)
				if err != nil {
					return
				}
			})
			t.Run("digest-512", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC || tcServer.deleteDisabled {
					return
				}
				se := *sd["sha512"]
				t.Parallel()
				repo := "digest-512"
				tag := "sha512"
				mTag1, mTag2 := "image1", "image2"
				// pull values out of the sample data entry to make the later tests easier to write
				if len(se.blob) != 4 {
					t.Fatalf("sha512 sample data must have 3 layers and one config blob")
				}
				if len(se.manifestList) != 2 {
					t.Fatalf("sha512 sample data must have 1 index and 1 manifest")
				}
				blobList := make([]digest.Digest, 0, len(se.blob))
				for dig := range se.blob {
					blobList = append(blobList, dig)
				}
				blobDig1 := blobList[0]
				blobDig2 := blobList[1]
				blobDig3 := blobList[2]
				blobDig4 := blobList[3]
				blob1 := se.blob[blobDig1]
				blob2 := se.blob[blobDig2]
				blob3 := se.blob[blobDig3]
				blob4 := se.blob[blobDig4]
				manDig := se.manifestList[0]
				indDig := se.manifestList[1]
				manBlob := se.manifest[manDig]
				indBlob := se.manifest[indDig]
				// push content
				// first blob is a standard post/put, depends on backend store able to change digest algorithm
				_, err := testAPIBlobPostPut(t, s, repo, blobDig1, blob1)
				if err != nil {
					t.Fatalf("failed to blob post: %v", err)
				}
				// second blob is a sha512 pushed with a POST/PATCH/PUT and digest-algorithm parameter
				u, err := url.Parse("/v2/" + repo + "/blobs/uploads/")
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				q := u.Query()
				q.Add("digest-algorithm", blobDig2.Algorithm().String())
				u.RawQuery = q.Encode()
				resp, err := testClientRun(t, s, "POST", u.String(), nil,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientRespStatus(http.StatusAccepted),
					testClientRespHeader("Location", ""))
				if err != nil {
					t.Fatalf("failed to run monolithic upload: %v", err)
				}
				loc := resp.Header().Get("Location")
				if loc == "" {
					t.Fatalf("location header missing in blob POST")
				}
				uLoc, err := url.Parse(loc)
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				u = u.ResolveReference(uLoc)
				resp, err = testClientRun(t, s, "PATCH", u.String(), blob2,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientReqHeader("Content-Length", fmt.Sprintf("%d", len(blob2))),
					testClientRespStatus(http.StatusAccepted))
				if err != nil {
					t.Errorf("failed to send blob patch: %v", err)
					return
				}
				loc = resp.Header().Get("Location")
				if loc == "" {
					t.Fatalf("location header missing in blob POST")
				}
				uLoc, err = url.Parse(loc)
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				u = u.ResolveReference(uLoc)
				q = u.Query()
				q.Add("digest", blobDig2.String())
				u.RawQuery = q.Encode()
				_, err = testClientRun(
					t, s, "PUT", u.String(), nil,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientRespHeader(types.HeaderDockerDigest, blobDig2.String()),
					testClientRespStatus(http.StatusCreated),
				)
				if err != nil {
					t.Fatalf("failed to send blob put: %v", err)
				}
				// third blob is a sha512 pushed with a POST/PATCH/PUT without digest-algorithm parameter
				u, err = url.Parse("/v2/" + repo + "/blobs/uploads/")
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				resp, err = testClientRun(t, s, "POST", u.String(), nil,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientRespStatus(http.StatusAccepted),
					testClientRespHeader("Location", ""))
				if err != nil {
					t.Fatalf("failed to run monolithic upload: %v", err)
				}
				loc = resp.Header().Get("Location")
				if loc == "" {
					t.Fatalf("location header missing in blob POST")
				}
				uLoc, err = url.Parse(loc)
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				u = u.ResolveReference(uLoc)
				resp, err = testClientRun(t, s, "PATCH", u.String(), blob3,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientReqHeader("Content-Length", fmt.Sprintf("%d", len(blob3))),
					testClientRespStatus(http.StatusAccepted))
				if err != nil {
					t.Errorf("failed to send blob patch: %v", err)
					return
				}
				loc = resp.Header().Get("Location")
				if loc == "" {
					t.Fatalf("location header missing in blob POST")
				}
				uLoc, err = url.Parse(loc)
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				u = u.ResolveReference(uLoc)
				q = u.Query()
				q.Add("digest", blobDig3.String())
				u.RawQuery = q.Encode()
				_, err = testClientRun(
					t, s, "PUT", u.String(), nil,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientRespHeader(types.HeaderDockerDigest, blobDig3.String()),
					testClientRespStatus(http.StatusCreated),
				)
				if err != nil {
					t.Fatalf("failed to send blob put: %v", err)
				}
				// fourth blob is pushed with a monolithic POST
				u, err = url.Parse("/v2/" + repo + "/blobs/uploads/")
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				q = u.Query()
				q.Add("digest", blobDig4.String())
				u.RawQuery = q.Encode()
				_, err = testClientRun(t, s, "POST", u.String(), blob4,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientRespStatus(http.StatusCreated),
					testClientRespHeader(types.HeaderDockerDigest, blobDig4.String()),
					testClientRespHeader("Location", ""))
				if err != nil {
					t.Fatalf("failed to run monolithic upload: %v", err)
				}
				// push manifest by sha512 digest with two tag args
				u, err = url.Parse("/v2/" + repo + "/manifests/" + manDig.String())
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				q = u.Query()
				q.Set("tag", mTag1)
				q.Add("tag", mTag2)
				u.RawQuery = q.Encode()
				_, err = testClientRun(
					t, s, "PUT", u.String(), manBlob,
					testClientReqHeader("Content-Type", types.MediaTypeOCI1Manifest),
					testClientRespStatus(http.StatusCreated),
					testClientRespHeader(types.HeaderDockerDigest, manDig.String()),
					testClientRespHeader("Location", ""),
					testClientRespHeader("OCI-Tag", mTag1, mTag2),
				)
				if err != nil {
					t.Fatalf("failed to manifest put by digest + tags: %v", err)
				}
				// push index by tag with sha512 digest arg
				u, err = url.Parse("/v2/" + repo + "/manifests/" + tag)
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				q = u.Query()
				q.Set("digest", indDig.String())
				u.RawQuery = q.Encode()
				_, err = testClientRun(
					t, s, "PUT", u.String(), indBlob,
					testClientReqHeader("Content-Type", types.MediaTypeOCI1ManifestList),
					testClientRespStatus(http.StatusCreated),
					testClientRespHeader(types.HeaderDockerDigest, indDig.String()),
					testClientRespHeader("Location", ""),
				)
				if err != nil {
					t.Fatalf("failed to manifest put by tag+digest: %v", err)
				}
				// pull content
				resp, err = testAPIManifestHead(t, s, repo, tag, indBlob)
				if err != nil {
					t.Errorf("failed to run manifest head by tag: %v", err)
				}
				d := resp.Header().Get(types.HeaderDockerDigest)
				if d != indDig.String() {
					t.Errorf("unexpected digest header, expected %s, received %s", indDig.String(), d)
				}
				_, err = testAPIManifestGet(t, s, repo, indDig.String(), indBlob)
				if err != nil {
					t.Errorf("failed to get index by digest: %v", err)
				}
				_, err = testAPIManifestGet(t, s, repo, manDig.String(), manBlob)
				if err != nil {
					t.Errorf("failed to get manifest by digest: %v", err)
				}
				_, err = testAPIManifestGet(t, s, repo, mTag1, manBlob)
				if err != nil {
					t.Errorf("failed to get manifest by tag %s: %v", mTag1, err)
				}
				_, err = testAPIManifestGet(t, s, repo, mTag2, manBlob)
				if err != nil {
					t.Errorf("failed to get manifest by tag %s: %v", mTag2, err)
				}
				_, err = testAPIBlobHead(t, s, repo, blobDig2)
				if err != nil {
					t.Errorf("failed to head layer2: %v", err)
				}
				_, err = testAPIBlobGet(t, s, repo, blobDig2, blob2)
				if err != nil {
					t.Errorf("failed to get layer2: %v", err)
				}
				_, err = testAPIBlobGet(t, s, repo, blobDig4, blob4)
				if err != nil {
					t.Errorf("failed to get config: %v", err)
				}
				// delete blobs by digest
				_, err = testAPIBlobRm(t, s, repo, blobDig1)
				if err != nil {
					t.Errorf("failed to delete layer1: %v", err)
				}
				_, err = testAPIBlobRm(t, s, repo, blobDig2)
				if err != nil {
					t.Errorf("failed to delete layer2: %v", err)
				}
				_, err = testAPIBlobRm(t, s, repo, blobDig4)
				if err != nil {
					t.Errorf("failed to delete config: %v", err)
				}
				// delete manifest by digest
				_, err = testAPIManifestRm(t, s, repo, manDig.String())
				if err != nil {
					t.Errorf("failed to delete manifest by digest: %v", err)
				}
				_, err = testAPIManifestRm(t, s, repo, indDig.String())
				if err != nil {
					t.Errorf("failed to delete index by digest: %v", err)
				}
			})
			t.Run("digest-algo-unknown", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC || tcServer.deleteDisabled {
					return
				}
				t.Parallel()
				repo := "digest-unknown"
				sd, err := genSampleData(t)
				if err != nil {
					t.Fatalf("failed to generate sample data")
				}
				// create a digest with an unknown algorithm
				badDig := "unknown:12345"
				// attempt to get/head blob
				u, err := url.Parse("/v2/" + repo + "/blobs/" + badDig)
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				_, err = testClientRun(t, s, "HEAD", u.String(), nil,
					testClientRespStatus(http.StatusNotFound, http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send head request: %v", err)
				}
				_, err = testClientRun(t, s, "GET", u.String(), nil,
					testClientRespStatus(http.StatusNotFound, http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send get request: %v", err)
				}
				// attempt to delete blob
				_, err = testClientRun(t, s, "DELETE", u.String(), nil,
					testClientRespStatus(http.StatusNotFound, http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send delete request: %v", err)
				}
				// attempt to push blob
				u, err = url.Parse("/v2/" + repo + "/blobs/uploads/?digest=" + url.QueryEscape(badDig))
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				_, err = testClientRun(t, s, "POST", u.String(), nil,
					testClientRespStatus(http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send blob post with digest: %v", err)
				}
				u, err = url.Parse("/v2/" + repo + "/blobs/uploads/?digest-algorithm=unknown")
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				_, err = testClientRun(t, s, "POST", u.String(), nil,
					testClientRespStatus(http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send blob post with algorithm: %v", err)
				}
				// attempt to get/head manifest
				u, err = url.Parse("/v2/" + repo + "/manifests/" + badDig)
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				_, err = testClientRun(t, s, "HEAD", u.String(), nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientRespStatus(http.StatusNotFound, http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send manifest head: %v", err)
				}
				_, err = testClientRun(t, s, "GET", u.String(), nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientRespStatus(http.StatusNotFound, http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send manifest get: %v", err)
				}
				// attempt to push manifest
				_, err = testClientRun(t, s, "PUT", u.String(), sd["image-amd64"].manifest[sd["image-amd64"].manifestList[0]],
					testClientReqHeader("Content-Type", types.MediaTypeOCI1Manifest),
					testClientRespStatus(http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send manifest put: %v", err)
				}
				u, err = url.Parse("/v2/" + repo + "/manifests/bad?digest=" + url.QueryEscape(badDig))
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				_, err = testClientRun(t, s, "PUT", u.String(), sd["image-amd64"].manifest[sd["image-amd64"].manifestList[0]],
					testClientReqHeader("Content-Type", types.MediaTypeOCI1Manifest),
					testClientRespStatus(http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send manifest put: %v", err)
				}
				// attempt to delete manifest
				_, err = testClientRun(t, s, "DELETE", u.String(), nil,
					testClientRespStatus(http.StatusNotFound, http.StatusBadRequest))
				if err != nil {
					t.Errorf("failed to send manifest delete: %v", err)
				}
			})
			t.Run("warning", func(t *testing.T) {
				if !tcServer.testWarn {
					return
				}
				t.Parallel()
				_, err := testClientRun(
					t, s, "GET", "/v2/"+existingRepo+"/tags/list", nil,
					testClientRespHeader("Warning", "299 - \""+warningMsg+"\""),
					testClientRespStatus(http.StatusOK),
				)
				if err != nil {
					return
				}
			})
			t.Run("invalid tag arg", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC {
					return
				}
				se := *sd["invalid-tag-arg"]
				t.Parallel()
				repo := "invalid-tag-arg"
				for dig, be := range se.blob {
					t.Run("BlobPostPut", func(t *testing.T) {
						_, err := testAPIBlobPostPut(t, s, repo, dig, be)
						if err != nil {
							t.Fatalf("failed to push blob: %v", err)
						}
					})
				}
				// pushing the invalid tag should fail
				tag := "invalid**tag--string"
				dig := se.manifestList[0]
				manifest := se.manifest[dig]
				mt := detectMediaType(manifest)
				_, err := testClientRun(
					t, s, "PUT", "/v2/"+repo+"/manifests/"+tag, manifest,
					testClientReqHeader("Content-Type", mt),
					testClientRespStatus(http.StatusBadRequest),
				)
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send manifest put: %v", err)
					}
				}
				// pulling the tag should also fail
				_, err = testClientRun(
					t, s, "GET", "/v2/"+repo+"/manifests/"+tag, nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientRespStatus(http.StatusBadRequest, http.StatusNotFound),
				)
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send manifest put: %v", err)
					}
				}
			})
			t.Run("invalid tag query param", func(t *testing.T) {
				if tcServer.readOnly || tcServer.testGC {
					return
				}
				se := *sd["invalid-tag-query-param"]
				t.Parallel()
				repo := "invalid-tag-query-param"
				for dig, be := range se.blob {
					t.Run("BlobPostPut", func(t *testing.T) {
						_, err := testAPIBlobPostPut(t, s, repo, dig, be)
						if err != nil {
							t.Fatalf("failed to push blob: %v", err)
						}
					})
				}
				// pushing the invalid tag should fail
				tag1 := "valid-tag"
				tag2 := "invalid**tag--string"
				dig := se.manifestList[0]
				manifest := se.manifest[dig]
				mt := detectMediaType(manifest)
				u, err := url.Parse("/v2/" + repo + "/manifests/" + dig.String())
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				q := u.Query()
				q.Set("tag", tag1)
				q.Add("tag", tag2)
				u.RawQuery = q.Encode()
				_, err = testClientRun(
					t, s, "PUT", u.String(), manifest,
					testClientReqHeader("Content-Type", mt),
					testClientRespStatus(http.StatusBadRequest),
				)
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send manifest put: %v", err)
					}
				}
				// pulling the digest should also fail
				_, err = testClientRun(
					t, s, "GET", "/v2/"+repo+"/manifests/"+dig.String(), nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientRespStatus(http.StatusBadRequest, http.StatusNotFound),
				)
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send manifest put: %v", err)
					}
				}
			})
			// TODO: test tag listing before and after pushing manifest
			// TODO: test blob chunked upload, stream upload, and blob mount
		})
	}
}

func TestAuth(t *testing.T) {
	t.Parallel()
	boolT := true
	boolF := false
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// repoNew := "newrepo" // only this repo is allowed for limited access
	repoCur := "testrepo"
	goodUser := "user"
	goodPass := "pass123"
	badUser := "hacker"
	badPass := "foobar"
	authGood, err := config.NewAuthBasicStatic(map[string]string{goodUser: goodPass}, false)
	if err != nil {
		t.Fatalf("failed to setup good auth: %v", err)
	}
	authAnon, err := config.NewAuthBasicStatic(map[string]string{goodUser: goodPass}, true)
	if err != nil {
		t.Fatalf("failed to setup anon auth: %v", err)
	}
	ttServer := []struct {
		name        string
		conf        config.Config
		allowAnon   bool
		allowRead   bool
		allowWrite  bool
		allowDelete bool
		authBasic   bool
		limitRepo   bool
	}{
		{
			name: "Basic Static",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					RootDir:   "./testdata",
					GC: config.ConfigGC{
						Frequency: -1 * time.Second,
					},
				},
				API: config.ConfigAPI{
					DeleteEnabled: &boolT,
					Blob: config.ConfigAPIBlob{
						DeleteEnabled: &boolF,
					},
					Referrer: config.ConfigAPIReferrer{
						Limit: 512 * 1024,
					},
				},
				Log:  logger,
				Auth: authGood,
			},
			allowAnon:   false,
			allowRead:   true,
			allowWrite:  true,
			allowDelete: true,
			authBasic:   true,
			limitRepo:   false,
		},
		{
			name: "Basic Static with Anon",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					RootDir:   "./testdata",
					GC: config.ConfigGC{
						Frequency: -1 * time.Second,
					},
				},
				API: config.ConfigAPI{
					DeleteEnabled: &boolT,
					Blob: config.ConfigAPIBlob{
						DeleteEnabled: &boolF,
					},
					Referrer: config.ConfigAPIReferrer{
						Limit: 512 * 1024,
					},
				},
				Log:  logger,
				Auth: authAnon,
			},
			allowAnon:   true,
			allowRead:   true,
			allowWrite:  true,
			allowDelete: true,
			authBasic:   true,
			limitRepo:   false,
		},
	}
	for _, tcServer := range ttServer {
		t.Run(tcServer.name, func(t *testing.T) {
			t.Parallel()
			// new server
			s := New(tcServer.conf)
			t.Cleanup(func() { _ = s.Close() })
			t.Run("unauth read", func(t *testing.T) {
				tcgList := []testClientGen{
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
				}
				// if allow anon, expect 200, else 401/403
				if tcServer.allowAnon {
					tcgList = append(tcgList, testClientRespStatus(http.StatusOK))
				} else {
					tcgList = append(tcgList, testClientRespStatus(http.StatusUnauthorized, http.StatusForbidden))
				}
				// request v1 manifest
				_, err := testClientRun(t, s, "GET", "/v2/"+repoCur+"/manifests/v1", nil, tcgList...)
				if err != nil {
					t.Errorf("unexpected response to anonymous read: %v", err)
				}
			})
			t.Run("basic auth read existing", func(t *testing.T) {
				if tcServer.limitRepo || tcServer.allowAnon || !tcServer.authBasic {
					return
				}
				// request v1 manifest
				_, err := testClientRun(
					t, s, "GET", "/v2/"+repoCur+"/manifests/v1", nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientReqHeader("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(goodUser+":"+goodPass))),
					testClientRespStatus(http.StatusOK),
				)
				if err != nil {
					t.Errorf("unexpected response to authenticated read: %v", err)
				}
			})
			t.Run("basic auth invalid user", func(t *testing.T) {
				if tcServer.limitRepo || tcServer.allowAnon || !tcServer.authBasic {
					return
				}
				// request v1 manifest
				_, err := testClientRun(
					t, s, "GET", "/v2/"+repoCur+"/manifests/v1", nil,
					testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
					testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
					testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
					testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
					testClientReqHeader("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(badUser+":"+badPass))),
					testClientRespStatus(http.StatusUnauthorized, http.StatusForbidden),
				)
				if err != nil {
					t.Errorf("unexpected response to invalid login: %v", err)
				}
			})
			// test blob delete returns method not allowed
			t.Run("basic auth delete blob", func(t *testing.T) {
				if tcServer.limitRepo || tcServer.allowAnon || !tcServer.authBasic {
					return
				}
				dig, err := digest.Canonical.FromString("test")
				if err != nil {
					t.Fatalf("failed to setup digest: %v", err)
				}
				// request v1 manifest
				_, err = testClientRun(
					t, s, "DELETE", "/v2/"+repoCur+"/blobs/"+dig.String(), nil,
					testClientReqHeader("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(goodUser+":"+goodPass))),
					testClientRespStatus(http.StatusMethodNotAllowed),
				)
				if err != nil {
					t.Errorf("unexpected response to delete blob: %v", err)
				}
			})
			// TODO: add more tests that also check repo access, writes, and deletes
			// TODO: test token auth
		})
	}
}

func TestRateLimit(t *testing.T) {
	t.Parallel()
	limit := 10
	s := New(config.Config{
		Storage: config.ConfigStorage{
			StoreType: config.StoreMem,
		},
		API: config.ConfigAPI{
			RateLimit: limit,
		},
	})
	t.Cleanup(func() { _ = s.Close() })
	reachedLimit := false
	for range limit * 2 {
		resp, err := testClientRun(t, s, "GET", "/v2/", nil)
		if err != nil {
			return
		}
		if resp.Result().StatusCode == http.StatusTooManyRequests {
			reachedLimit = true
			if resp.Header().Get("Retry-After") != "1" {
				t.Errorf("unexpected Retry-After header, expected 1, received %s", resp.Header().Get("Retry-After"))
			}
		} else if resp.Result().StatusCode != http.StatusOK {
			t.Errorf("unexpected status, expected %d, received %d", http.StatusOK, resp.Result().StatusCode)
		}
	}
	if !reachedLimit {
		t.Errorf("never reached rate limit")
	}
	_, err := testClientRun(t, s, "GET", "/v2/", nil,
		testClientReqHeader("X-Forwarded-For", "127.0.0.2"),
		testClientRespStatus(http.StatusOK))
	if err != nil {
		return
	}
}

func TestMatchV2(t *testing.T) {
	t.Parallel()
	tt := []struct {
		name        string
		pathEl      []string
		params      []string
		expectMatch []string
		expectOK    bool
	}{
		{
			name:     "Mismatch v2",
			pathEl:   []string{"v3", "repo", "manifests", "latest"},
			params:   []string{"...", "manifests", "*"},
			expectOK: false,
		},
		{
			name:     "Mismatch string",
			pathEl:   []string{"v2", "repo", "manifests", "latest"},
			params:   []string{"...", "blobs", "*"},
			expectOK: false,
		},
		{
			name:     "Mismatch extra params",
			pathEl:   []string{"v2", "repo", "manifests", "latest", "extra"},
			params:   []string{"...", "manifests", "*"},
			expectOK: false,
		},
		{
			name:     "Mismatch short params",
			pathEl:   []string{"v2", "repo", "manifests"},
			params:   []string{"...", "manifests", "*"},
			expectOK: false,
		},
		{
			name:     "Mismatch empty repo",
			pathEl:   []string{"v2", "manifests", "latest"},
			params:   []string{"...", "manifests", "*"},
			expectOK: false,
		},
		{
			name:     "Invalid repo",
			pathEl:   []string{"v2", "Repo", "manifests", "latest"},
			params:   []string{"...", "manifests", "*"},
			expectOK: false,
		},
		{
			name:        "Single repo path",
			pathEl:      []string{"v2", "repo", "manifests", "latest"},
			params:      []string{"...", "manifests", "*"},
			expectMatch: []string{"repo", "latest"},
			expectOK:    true,
		},
		{
			name:        "Multi repo path",
			pathEl:      []string{"v2", "repo", "with", "sub", "path", "manifests", "latest"},
			params:      []string{"...", "manifests", "*"},
			expectMatch: []string{"repo/with/sub/path", "latest"},
			expectOK:    true,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			match, ok := matchV2(tc.pathEl, tc.params...)
			if tc.expectOK != ok {
				t.Errorf("unexpected OK: expected %t, received %t", tc.expectOK, ok)
			}
			if !tc.expectOK {
				return
			}
			allMatch := len(tc.expectMatch) == len(match)
			if allMatch {
				for i, val := range tc.expectMatch {
					if match[i] != val {
						allMatch = false
					}
				}
			}
			if !allMatch {
				t.Errorf("match did not match: expected %v, received %v", tc.expectMatch, match)
			}
		})
	}
}

type (
	testClient    func(req *http.Request) (*httptest.ResponseRecorder, error)
	testClientGen func(t *testing.T, next testClient) testClient
)

var errValidationFailed = fmt.Errorf("validation failed")

func testClientRun(t *testing.T, s http.Handler, method, path string, body []byte, tcgList ...testClientGen) (*httptest.ResponseRecorder, error) {
	t.Helper()
	if body == nil {
		body = []byte{}
	}
	req, err := http.NewRequest(method, path, io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return nil, err
	}
	// final testClient serves the request to a recorder
	tc := func(req *http.Request) (*httptest.ResponseRecorder, error) {
		resp := httptest.NewRecorder()
		s.ServeHTTP(resp, req)
		return resp, nil
	}
	// chain all the testClient funcs together
	for _, tcg := range tcgList {
		tc = tcg(t, tc)
	}
	resp, err := tc(req)
	if err != nil {
		if resp.Code >= 300 && resp.Body.Len() > 0 {
			t.Errorf("failing request body: %s", resp.Body.Bytes())
		}
		t.Errorf("failed running %s to %s", method, path)
	}
	return resp, err
}

func testClientReqHeader(k, v string) testClientGen {
	return func(t *testing.T, next testClient) testClient {
		return func(req *http.Request) (*httptest.ResponseRecorder, error) {
			t.Helper()
			req.Header.Add(k, v)
			return next(req)
		}
	}
}

func testClientRespStatus(codes ...int) testClientGen {
	return func(t *testing.T, next testClient) testClient {
		return func(req *http.Request) (*httptest.ResponseRecorder, error) {
			t.Helper()
			resp, err := next(req)
			if !slices.Contains(codes, resp.Code) {
				t.Errorf("unexpected status code: %d, expected %v", resp.Code, codes)
				err = errValidationFailed
			}
			return resp, err
		}
	}
}

func testClientRespBody(body []byte) testClientGen {
	return func(t *testing.T, next testClient) testClient {
		return func(req *http.Request) (*httptest.ResponseRecorder, error) {
			t.Helper()
			resp, err := next(req)
			if !bytes.Equal(body, resp.Body.Bytes()) {
				t.Errorf("body mismatch")
				err = errValidationFailed
			}
			return resp, err
		}
	}
}

func testClientRespHeader(k string, vList ...string) testClientGen {
	return func(t *testing.T, next testClient) testClient {
		return func(req *http.Request) (*httptest.ResponseRecorder, error) {
			t.Helper()
			resp, err := next(req)
			respV := resp.Header().Values(k)
			for _, v := range vList {
				if v == "" && len(vList) == 1 {
					continue
				}
				if !slices.Contains(respV, v) {
					t.Errorf("header mismatch for %s, expected %s, received %v", k, v, respV)
					err = errValidationFailed
				}
			}
			// if value not set, just verify existence of header
			if (len(vList) == 0 || vList[0] == "") && len(respV) == 0 {
				t.Errorf("header missing for %s", k)
				err = errValidationFailed
			}
			return resp, err
		}
	}
}

func testSampleEntryPush(t *testing.T, s *Server, se sampleEntry, repo, tag string) error {
	t.Helper()
	var err error
	for dig, be := range se.blob {
		t.Run("BlobPostPut", func(t *testing.T) {
			_, err = testAPIBlobPostPut(t, s, repo, dig, be)
		})
		if err != nil {
			return err
		}
	}
	for i, dig := range se.manifestList {
		digOrTag := dig.String()
		if i == len(se.manifestList)-1 && tag != "" {
			digOrTag = tag
		}
		t.Run("ManifestPut", func(t *testing.T) {
			_, err = testAPIManifestPut(t, s, repo, digOrTag, se.manifest[dig])
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func testSampleEntryPull(t *testing.T, s *Server, se sampleEntry, repo, tag string) error {
	t.Helper()
	var err error
	for i, dig := range se.manifestList {
		digOrTag := dig.String()
		if i == len(se.manifestList)-1 && tag != "" {
			digOrTag = tag
		}
		t.Run("ManifestGet", func(t *testing.T) {
			_, err = testAPIManifestGet(t, s, repo, digOrTag, se.manifest[dig])
		})
		if err != nil {
			return err
		}
	}
	for dig, be := range se.blob {
		t.Run("BlobGet", func(t *testing.T) {
			_, err = testAPIBlobGet(t, s, repo, dig, be)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func testAPITagsList(t *testing.T, s *Server, repo string, body []byte) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{
		testClientRespStatus(http.StatusOK),
	}
	if body != nil {
		tcgList = append(tcgList, testClientRespBody(body))
	}
	resp, err := testClientRun(t, s, "GET", "/v2/"+repo+"/tags/list", nil, tcgList...)
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send tag list: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

func testAPIManifestGet(t *testing.T, s *Server, repo string, digOrTag string, body []byte) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{
		testClientRespStatus(http.StatusOK),
		testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
		testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
		testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
		testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
	}
	if body != nil {
		tcgList = append(tcgList, testClientRespBody(body))
		if mt := detectMediaType(body); mt != "" {
			tcgList = append(tcgList, testClientRespHeader("Content-Type", mt))
		}
	}
	resp, err := testClientRun(t, s, "GET", "/v2/"+repo+"/manifests/"+digOrTag, nil, tcgList...)
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send manifest get: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

func testAPIManifestHead(t *testing.T, s *Server, repo string, digOrTag string, body []byte) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{
		testClientRespStatus(http.StatusOK),
		testClientReqHeader("Accept", types.MediaTypeOCI1Manifest),
		testClientReqHeader("Accept", types.MediaTypeOCI1ManifestList),
		testClientReqHeader("Accept", types.MediaTypeDocker2Manifest),
		testClientReqHeader("Accept", types.MediaTypeDocker2ManifestList),
	}
	if body != nil {
		if mt := detectMediaType(body); mt != "" {
			tcgList = append(tcgList, testClientRespHeader("Content-Type", mt))
		}
	}
	resp, err := testClientRun(t, s, "HEAD", "/v2/"+repo+"/manifests/"+digOrTag, nil, tcgList...)
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send manifest head: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

func testAPIManifestPut(t *testing.T, s *Server, repo string, digOrTag string, manifest []byte) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{
		testClientRespStatus(http.StatusCreated),
		testClientRespHeader("Location", ""),
	}
	if strings.Contains(digOrTag, ":") {
		tcgList = append(
			tcgList,
			testClientRespHeader(types.HeaderDockerDigest, digOrTag),
		)
	}
	if mt := detectMediaType(manifest); mt != "" {
		tcgList = append(tcgList, testClientReqHeader("Content-Type", mt))
	}
	resp, err := testClientRun(t, s, "PUT", "/v2/"+repo+"/manifests/"+digOrTag, manifest, tcgList...)
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send manifest put: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

func testAPIManifestRm(t *testing.T, s *Server, repo string, digOrTag string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{
		testClientRespStatus(http.StatusAccepted),
	}
	resp, err := testClientRun(t, s, "DELETE", "/v2/"+repo+"/manifests/"+digOrTag, nil, tcgList...)
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send manifest delete: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

func testAPIBlobGet(t *testing.T, s *Server, repo string, dig digest.Digest, body []byte) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{testClientRespStatus(http.StatusOK)}
	if body != nil {
		tcgList = append(tcgList, testClientRespBody(body))
	}
	resp, err := testClientRun(t, s, "GET", "/v2/"+repo+"/blobs/"+dig.String(), nil, tcgList...)
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send blob get: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

func testAPIBlobHead(t *testing.T, s *Server, repo string, dig digest.Digest) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{
		testClientRespStatus(http.StatusOK),
		testClientRespHeader(types.HeaderDockerDigest, dig.String()),
	}
	resp, err := testClientRun(t, s, "HEAD", "/v2/"+repo+"/blobs/"+dig.String(), nil, tcgList...)
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send blob head: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

func testAPIBlobPostPut(t *testing.T, s *Server, repo string, dig digest.Digest, blob []byte) (*httptest.ResponseRecorder, error) {
	t.Helper()
	u, err := url.Parse("/v2/" + repo + "/blobs/uploads/")
	if err != nil {
		t.Errorf("failed to parse blob post url: %v", err)
		return nil, err
	}
	resp, err := testClientRun(t, s, "POST", u.String(), nil,
		testClientReqHeader("Content-Type", "application/octet-stream"),
		testClientRespStatus(http.StatusAccepted),
		testClientRespHeader("Location", ""))
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send blob post: %v", err)
		}
		return nil, err
	}
	loc := resp.Header().Get("Location")
	if loc == "" {
		t.Errorf("location header missing on blob POST")
		return nil, errValidationFailed
	}
	uLoc, err := url.Parse(loc)
	if err != nil {
		t.Errorf("failed to parse location header URL fragment %s: %v", loc, err)
		return nil, err
	}
	u = u.ResolveReference(uLoc)
	q := u.Query()
	q.Add("digest", dig.String())
	u.RawQuery = q.Encode()
	resp, err = testClientRun(t, s, "PUT", u.String(), blob,
		testClientReqHeader("Content-Type", "application/octet-stream"),
		testClientReqHeader("Content-Length", fmt.Sprintf("%d", len(blob))),
		testClientRespHeader(types.HeaderDockerDigest, dig.String()),
		testClientRespStatus(http.StatusCreated))
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send blob put: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

func testAPIBlobRm(t *testing.T, s *Server, repo string, dig digest.Digest) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{testClientRespStatus(http.StatusAccepted)}
	resp, err := testClientRun(t, s, "DELETE", "/v2/"+repo+"/blobs/"+dig.String(), nil, tcgList...)
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to delete blob: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

func testAPIReferrersList(t *testing.T, s *Server, repo string, dig digest.Digest, filterArtifactType string, body []byte) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{
		testClientRespStatus(http.StatusOK),
		testClientRespHeader("Content-Type", types.MediaTypeOCI1ManifestList),
	}
	if body != nil {
		tcgList = append(tcgList, testClientRespBody(body))
	}
	path := "/v2/" + repo + "/referrers/" + dig.String()
	if filterArtifactType != "" {
		path = path + "?artifactType=" + filterArtifactType
		tcgList = append(tcgList, testClientRespHeader("OCI-Filters-Applied", "artifactType"))
	}
	resp, err := testClientRun(t, s, "GET", path, nil, tcgList...)
	if err != nil {
		if !errors.Is(err, errValidationFailed) {
			t.Errorf("failed to send referrers list: %v", err)
		}
		return nil, err
	}
	return resp, nil
}

type (
	sampleData  map[string]*sampleEntry
	sampleEntry struct {
		manifest     map[digest.Digest][]byte
		blob         map[digest.Digest][]byte
		manifestList []digest.Digest
	}
)

const (
	layerPerImage      = 2
	layerPerReferrer   = 1
	layerSize          = 1024
	emptyJSON          = "{}"
	sampleArtifactType = "application/vnd.example.artifact+json"
)

func newSampleEntry() sampleEntry {
	return sampleEntry{
		manifest:     map[digest.Digest][]byte{},
		blob:         map[digest.Digest][]byte{},
		manifestList: []digest.Digest{},
	}
}

var imagePlatforms = []string{"amd64", "arm64"}

func genSampleData(t *testing.T) (sampleData, error) {
	now := time.Now()
	seed := now.UnixNano()
	t.Logf("random seed for sample data initialized to %d", seed)
	rng := rand.New(rand.NewSource(seed))
	sd := sampleData{}
	// setup example image manifests
	imageCount := len(imagePlatforms)
	entryImages := make([]*sampleEntry, imageCount)
	for i := range imageCount {
		entry := newSampleEntry()
		_, _, err := entry.genSampleImage(genSampleImageOpts{
			platform: types.Platform{
				OS:           "linux",
				Architecture: imagePlatforms[i],
			},
		})
		if err != nil {
			return nil, err
		}
		entryImages[i] = &entry
		sd[fmt.Sprintf("image-%s", imagePlatforms[i])] = &entry
	}

	// create the image with 4 layers: foreign compressed, foreign uncompressed, foreign docker, and layer with a URL
	entryForeign := newSampleEntry()
	_, _, err := entryForeign.genSampleImage(genSampleImageOpts{
		rng: rng,
		layerOpts: []genSampleLayerOpts{
			{
				foreign:     true,
				size:        layerSize,
				mt:          types.MediaTypeOCI1ForeignLayerGzip,
				foreignURLs: []string{"https://store.example.com/blobs/foreign1"},
			},
			{
				foreign:     true,
				size:        layerSize,
				mt:          types.MediaTypeOCI1ForeignLayer,
				compFn:      func(w io.Writer) io.WriteCloser { return nopWriteCloser{Writer: w} },
				foreignURLs: []string{"https://store.example.com/blobs/foreign2"},
			},
			{
				foreign:     true,
				size:        layerSize,
				mt:          types.MediaTypeDocker2ForeignLayer,
				foreignURLs: []string{"https://store.example.com/blobs/foreign3"},
			},
			{
				foreign:     true,
				size:        layerSize,
				mt:          types.MediaTypeOCI1LayerGzip,
				foreignURLs: []string{"https://store.example.com/blobs/foreign4"},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	sd["image-foreign"] = &entryForeign

	// setup example index
	entryIndex := newSampleEntry()
	// copy images into index entry
	for i := range imageCount {
		maps.Copy(entryIndex.blob, entryImages[i].blob)
		maps.Copy(entryIndex.manifest, entryImages[i].manifest)
		entryIndex.manifestList = append(entryIndex.manifestList, entryImages[i].manifestList...)
	}
	indDescList := make([]types.Descriptor, imageCount)
	for l := range imageCount {
		indDescList[l] = types.Descriptor{
			MediaType: types.MediaTypeOCI1Manifest,
			Size:      int64(len(entryImages[l].manifest[entryImages[l].manifestList[0]])),
			Digest:    entryImages[l].manifestList[0],
			Platform: &types.Platform{
				OS:           "linux",
				Architecture: imagePlatforms[l],
			},
		}
	}
	_, _, err = entryIndex.genSampleIndex(genSampleIndexOpts{
		rng:       rng,
		imageDesc: indDescList,
		annotations: map[string]string{
			"config-type": "test",
		},
	})
	if err != nil {
		return nil, err
	}
	sd["index"] = &entryIndex

	// sha512 sample index with a single image containing 3 layers
	entrySha512 := newSampleEntry()
	_, _, err = entrySha512.genSampleIndex(genSampleIndexOpts{
		rng:     rng,
		digAlgo: digest.SHA512,
		imageOpts: []genSampleImageOpts{
			{
				layerOpts: []genSampleLayerOpts{
					{}, {}, {},
				},
				platform: types.Platform{
					OS:           "linux",
					Architecture: "amd64",
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	sd["sha512"] = &entrySha512

	// various other sample images
	for _, name := range []string{"invalid-tag-arg", "invalid-tag-query-param"} {
		entry := newSampleEntry()
		_, _, err := entry.genSampleImage(genSampleImageOpts{rng: rng})
		if err != nil {
			return nil, err
		}
		sd[name] = &entry
	}

	// TODO: setup example referrers: config with content, empty config, annotation only

	return sd, nil
}

type genSampleIndexOpts struct {
	rng          *rand.Rand
	artifactType string
	annotations  map[string]string
	digAlgo      digest.Algorithm
	imageDesc    []types.Descriptor
	imageOpts    []genSampleImageOpts
}

func (se *sampleEntry) genSampleIndex(opts genSampleIndexOpts) (digest.Digest, int64, error) {
	if opts.digAlgo.IsZero() {
		opts.digAlgo = digest.Canonical
	}
	if len(opts.imageDesc) == 0 {
		for _, imageOpt := range opts.imageOpts {
			if imageOpt.rng == nil {
				imageOpt.rng = opts.rng
			}
			if imageOpt.digAlgo.IsZero() {
				imageOpt.digAlgo = opts.digAlgo
			}
			if imageOpt.platform.OS == "" || imageOpt.platform.Architecture == "" {
				imageOpt.platform.OS = "linux"
				imageOpt.platform.Architecture = "amd64"
			}
			manDig, manSize, err := se.genSampleImage(imageOpt)
			if err != nil {
				return digest.Digest{}, 0, err
			}
			opts.imageDesc = append(opts.imageDesc, types.Descriptor{
				MediaType:   types.MediaTypeOCI1Manifest,
				Digest:      manDig,
				Size:        manSize,
				Platform:    &imageOpt.platform,
				Annotations: imageOpt.annotations,
			})
		}
	}
	ind := types.Index{
		SchemaVersion: 2,
		MediaType:     types.MediaTypeOCI1ManifestList,
		ArtifactType:  opts.artifactType,
		Manifests:     opts.imageDesc,
		Annotations:   opts.annotations,
	}
	indJSON, err := json.Marshal(ind)
	if err != nil {
		return digest.Digest{}, 0, err
	}
	indDig, err := digest.Canonical.FromBytes(indJSON)
	if err != nil {
		return digest.Digest{}, 0, err
	}
	se.manifest[indDig] = indJSON
	se.manifestList = append(se.manifestList, indDig)
	return indDig, int64(len(indJSON)), nil
}

type genSampleImageOpts struct {
	rng          *rand.Rand
	artifactType string
	annotations  map[string]string
	digAlgo      digest.Algorithm
	confDesc     types.Descriptor
	layerDesc    []types.Descriptor
	layerOpts    []genSampleLayerOpts
	platform     types.Platform
}

func (se *sampleEntry) genSampleImage(opts genSampleImageOpts) (digest.Digest, int64, error) {
	if opts.digAlgo.IsZero() {
		opts.digAlgo = digest.Canonical
	}
	if opts.platform.OS == "" || opts.platform.Architecture == "" {
		opts.platform = types.Platform{
			OS:           "linux",
			Architecture: "amd64",
		}
	}

	if len(opts.layerDesc) == 0 || opts.confDesc.Digest.IsZero() {
		if len(opts.layerOpts) == 0 {
			opts.layerOpts = make([]genSampleLayerOpts, 2)
		}
		now := time.Now()
		conf := sampleImage{
			Create:   &now,
			Platform: opts.platform,
			Config: sampleConfig{
				User:       "root",
				Env:        []string{"PATH=/"},
				Entrypoint: []string{"/entrypoint.sh"},
				Cmd:        []string{"hello", "world"},
				WorkingDir: "/",
				Labels: map[string]string{
					"config-type": "test",
				},
			},
			RootFS: sampleRootFS{
				Type:    "layers",
				DiffIDs: make([]digest.Digest, len(opts.layerOpts)),
			},
		}
		opts.layerDesc = make([]types.Descriptor, len(opts.layerOpts))
		for l, layerOpt := range opts.layerOpts {
			if layerOpt.mt == "" {
				layerOpt.mt = types.MediaTypeOCI1LayerGzip
			}
			if layerOpt.rng == nil {
				layerOpt.rng = opts.rng
			}
			if layerOpt.digAlgo.IsZero() {
				layerOpt.digAlgo = opts.digAlgo
			}
			digOrig, digComp, compSize, err := se.genSampleLayer(layerOpt)
			if err != nil {
				return digest.Digest{}, 0, err
			}
			conf.RootFS.DiffIDs[l] = digOrig
			opts.layerDesc[l] = types.Descriptor{
				MediaType: layerOpt.mt,
				Size:      compSize,
				Digest:    digComp,
				URLs:      layerOpt.foreignURLs,
			}
		}
		confJSON, err := json.Marshal(conf)
		if err != nil {
			return digest.Digest{}, 0, err
		}
		confDig, err := opts.digAlgo.FromBytes(confJSON)
		if err != nil {
			return digest.Digest{}, 0, err
		}
		se.blob[confDig] = confJSON
		opts.confDesc = types.Descriptor{
			MediaType: types.MediaTypeOCI1ImageConfig,
			Size:      int64(len(confJSON)),
			Digest:    confDig,
		}
	}

	man := types.Manifest{
		SchemaVersion: 2,
		MediaType:     types.MediaTypeOCI1Manifest,
		ArtifactType:  opts.artifactType,
		Config:        opts.confDesc,
		Layers:        opts.layerDesc,
		Annotations:   opts.annotations,
	}
	manJSON, err := json.Marshal(man)
	if err != nil {
		return digest.Digest{}, 0, err
	}
	manDig, err := opts.digAlgo.FromBytes(manJSON)
	if err != nil {
		return digest.Digest{}, 0, err
	}
	se.manifest[manDig] = manJSON
	se.manifestList = append(se.manifestList, manDig)
	return manDig, int64(len(manJSON)), nil
}

type genSampleLayerOpts struct {
	rng         *rand.Rand
	mt          string
	size        int
	digAlgo     digest.Algorithm
	compFn      func(io.Writer) io.WriteCloser
	foreign     bool
	foreignURLs []string
}

func (se *sampleEntry) genSampleLayer(opts genSampleLayerOpts) (digest.Digest, digest.Digest, int64, error) {
	// init unset opts
	if opts.rng == nil {
		opts.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if opts.size <= 0 {
		opts.size = 10240
	}
	if opts.digAlgo.IsZero() {
		opts.digAlgo = digest.Canonical
	}
	if opts.compFn == nil {
		opts.compFn = func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) }
	}

	layerOrigBytes := make([]byte, opts.size)
	if _, err := opts.rng.Read(layerOrigBytes); err != nil {
		return digest.Digest{}, digest.Digest{}, 0, err
	}
	var layerCompBuf bytes.Buffer
	compW := opts.compFn(&layerCompBuf)
	if _, err := compW.Write(layerOrigBytes); err != nil {
		return digest.Digest{}, digest.Digest{}, 0, err
	}
	if err := compW.Close(); err != nil {
		return digest.Digest{}, digest.Digest{}, 0, err
	}
	layerCompBytes := layerCompBuf.Bytes()
	digOrig, err := opts.digAlgo.FromBytes(layerOrigBytes)
	if err != nil {
		return digest.Digest{}, digest.Digest{}, 0, err
	}
	digComp, err := opts.digAlgo.FromBytes(layerCompBytes)
	if err != nil {
		return digest.Digest{}, digest.Digest{}, 0, err
	}
	if !opts.foreign {
		se.blob[digComp] = layerCompBytes
	}
	return digOrig, digComp, int64(len(layerCompBytes)), nil
}

// sample types are partial schemas for image configs
type sampleImage struct {
	Create *time.Time `json:"created,omitempty"`
	types.Platform
	Config sampleConfig `json:"config"`
	RootFS sampleRootFS `json:"rootfs"`
}

type sampleConfig struct {
	User       string            `json:"User,omitempty"`
	Env        []string          `json:"Env,omitempty"`
	Entrypoint []string          `json:"Entrypoint,omitempty"`
	Cmd        []string          `json:"Cmd,omitempty"`
	WorkingDir string            `json:"WorkingDir,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
}

type sampleRootFS struct {
	Type    string          `json:"type"`
	DiffIDs []digest.Digest `json:"diff_ids"`
}

func detectMediaType(b []byte) string {
	detect := struct {
		MediaType string `json:"mediaType,omitempty"`
	}{}
	_ = json.Unmarshal(b, &detect)
	return detect.MediaType
}

var linkRegexp = regexp.MustCompile(`^<([^>]+)>; rel=next$`)

func parseLinkHeader(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if match := linkRegexp.FindStringSubmatch(s); len(match) > 1 {
		return match[1], nil
	}
	return "", fmt.Errorf("failed to parse link header: %s", s)
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error {
	return nil
}
