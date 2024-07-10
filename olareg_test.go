package olareg

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	// imports required for go-digest
	_ "crypto/sha256"
	_ "crypto/sha512"

	"github.com/opencontainers/go-digest"

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
	grace := time.Millisecond * 250
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
	ttServer := []struct {
		name     string
		conf     config.Config
		existing bool
		readOnly bool
		testGC   bool
	}{
		{
			name: "Mem",
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
				},
			},
			testGC: true,
		},
		{
			name: "Mem with Dir",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					RootDir:   "./testdata",
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
			},
			existing: true,
			testGC:   true,
		},
		{
			name: "Dir",
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
				},
			},
			existing: true,
			readOnly: true,
		},
	}
	for _, tcServer := range ttServer {
		tcServer := tcServer
		t.Run(tcServer.name, func(t *testing.T) {
			t.Parallel()
			// new server
			s := New(tcServer.conf)
			t.Cleanup(func() { _ = s.Close() })
			t.Run("Unknown Method", func(t *testing.T) {
				t.Parallel()
				if _, err := testClientRun(t, s, "GET", "/unknown/url", nil,
					testClientRespStatus(http.StatusNotFound),
				); err != nil {
					t.Errorf("unknown URL: %v", err)
				}

				if _, err := testClientRun(t, s, "GET", "/v2/unknown/method", nil,
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

				resp, err := testClientRun(t, s, "GET", "/v2/missing/tags/list", nil,
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
				resp, err := testClientRun(t, s, "GET", "/../../../v2/"+existingRepo+"/tags/list", nil,
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
				if !tcServer.existing {
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
				if rr.MediaType != types.MediaTypeOCI1ManifestList || rr.SchemaVersion != 2 || len(rr.Manifests) != 0 {
					t.Errorf("referrers response should be an index, schema 2, with %d manifests: %v", 0, rr)
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
				if tcServer.readOnly {
					return
				}
				t.Parallel()
				if err := testSampleEntryPush(t, s, *sd["image-amd64"], "push-amd64", "v1"); err != nil {
					return
				}
				_ = testSampleEntryPull(t, s, *sd["image-amd64"], "push-amd64", "v1")
			})
			t.Run("Index", func(t *testing.T) {
				if tcServer.readOnly {
					return
				}
				t.Parallel()
				if err := testSampleEntryPush(t, s, *sd["index"], "index", "index"); err != nil {
					return
				}
				_ = testSampleEntryPull(t, s, *sd["index"], "index", "index")
			})
			t.Run("Read Only Push", func(t *testing.T) {
				if !tcServer.readOnly {
					return
				}
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
				if mt := detectMediaType(sd["index"].manifest[sd["index"].manifestList[0]]); mt != "" {
					tcgList = append(tcgList, testClientReqHeader("Content-Type", mt))
				}
				_, err = testClientRun(t, s, "PUT", "/v2/read-write/manifests/latest", sd["index"].manifest[sd["index"].manifestList[0]], tcgList...)
				if err != nil && !errors.Is(err, errValidationFailed) {
					t.Errorf("failed to send manifest put: %v", err)
				}
			})
			t.Run("Garbage Collect", func(t *testing.T) {
				if !tcServer.testGC || tcServer.readOnly {
					return
				}
				t.Parallel()
				// push two images with three tags
				if err := testSampleEntryPush(t, s, *sd["image-amd64"], "gc", "amd64"); err != nil {
					return
				}
				if err := testSampleEntryPush(t, s, *sd["image-amd64"], "gc", "amd64-copy"); err != nil {
					return
				}
				if err := testSampleEntryPush(t, s, *sd["image-arm64"], "gc", "arm64"); err != nil {
					return
				}
				// delete one manifest by digest
				if _, err := testAPIManifestRm(t, s, "gc", sd["image-arm64"].manifestList[0].String()); err != nil {
					t.Fatalf("failed to remove manifest: %v", err)
				}
				// delete the other manifest by tag
				if _, err := testAPIManifestRm(t, s, "gc", "amd64-copy"); err != nil {
					t.Fatalf("failed to remove manifest: %v", err)
				}
				// delay for GC
				for retry := 0; retry < 10; retry++ {
					time.Sleep(sleep + (time.Millisecond * 200 * time.Duration(retry)))
					remaining := false
					for dig := range sd["image-arm64"].blob {
						if _, ok := sd["image-amd64"].blob[dig]; ok {
							continue // skip dup blobs
						}
						resp, err := testClientRun(t, s, "HEAD", "/v2/gc/blobs/"+string(dig), nil)
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
				if err := testSampleEntryPull(t, s, *sd["image-amd64"], "gc", "amd64"); err != nil {
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
				for dig := range sd["image-arm64"].blob {
					if _, ok := sd["image-amd64"].blob[dig]; ok {
						continue // skip dup blobs
					}
					if _, err := testClientRun(t, s, "GET", "/v2/gc/blobs/"+dig.String(), nil,
						testClientRespStatus(http.StatusNotFound)); err != nil {
						t.Errorf("did not receive a not-found error on a GC blob: %v", err)
					} else {
						t.Logf("blog GC verified: %s", dig.String())
					}
				}
				for dig := range sd["image-amd64"].blob {
					if _, err := testClientRun(t, s, "HEAD", "/v2/gc/blobs/"+dig.String(), nil,
						testClientRespStatus(http.StatusOK)); err != nil {
						t.Errorf("GC blob of image that should have been preserved: %v", err)
					} else {
						t.Logf("blog retention verified: %s", dig.String())
					}
				}
			})
			t.Run("Blob Push Monolithic", func(t *testing.T) {
				if tcServer.readOnly {
					return
				}
				t.Parallel()
				repo := "monolithic"
				// send good blob
				exBlob := []byte(`example monolithic content`)
				exDigGood := digest.Canonical.FromBytes(exBlob)
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
				exDigBad := digest.Canonical.FromString("bad blob digest")
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
				if tcServer.readOnly {
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
					testClientRespStatus(http.StatusAccepted))
				if err != nil {
					if !errors.Is(err, errValidationFailed) {
						t.Errorf("failed to send blob delete: %v", err)
					}
					return
				}
			})
			t.Run("Corrupt Repo", func(t *testing.T) {
				if !tcServer.existing {
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
				_, err = testClientRun(t, s, "GET", "/v2/"+corruptRepo+"/manifests/"+corruptTag, nil,
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
				_, err = testClientRun(t, s, "GET", "/v2/"+corruptRepo+"/manifests/"+dig.String(), nil,
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
				emptyBlobDig := digest.Canonical.FromBytes(emptyBlobBytes)
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
				referrerDig := digest.Canonical.FromBytes(referrerBytes)
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
			t.Run("Referrers pagination", func(t *testing.T) {
				if tcServer.readOnly {
					return
				}
				t.Parallel()
				count := 25
				// push sample image
				if err := testSampleEntryPush(t, s, *sd["index"], "referrer", "index"); err != nil {
					return
				}
				// generate referrer manifest with annotations, push blob
				emptyBlob := []byte(`{}`)
				emptyDig := digest.Canonical.FromBytes(emptyBlob)
				if _, err := testAPIBlobPostPut(t, s, "referrer", emptyDig, emptyBlob); err != nil {
					return
				}
				exBlob := []byte(`example artifact content`)
				exDig := digest.Canonical.FromBytes(exBlob)
				if _, err := testAPIBlobPostPut(t, s, "referrer", exDig, exBlob); err != nil {
					return
				}
				subDig := sd["index"].manifestList[len(sd["index"].manifestList)-1]
				subLen := int64(len(sd["index"].manifest[subDig]))
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
				for i := 0; i < count; i++ {
					artMan.Annotations["count"] = fmt.Sprintf("%d", i)
					artBytes, err := json.Marshal(artMan)
					if err != nil {
						t.Fatalf("failed to marshal artifact")
					}
					artDig := digest.Canonical.FromBytes(artBytes)
					if _, err := testAPIManifestPut(t, s, "referrer", artDig.String(), artBytes); err != nil {
						return
					}
				}
				// query for referrer, verify link header
				refIdx := types.Index{}
				found := map[string]bool{}
				pageCount := 1
				tcgList := []testClientGen{
					testClientRespStatus(http.StatusOK),
					testClientRespHeader("Content-Type", types.MediaTypeOCI1ManifestList),
				}
				path := "/v2/referrer/referrers/" + subDig.String()
				resp, err := testClientRun(t, s, "GET", path, nil, tcgList...)
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
				artDig := digest.Canonical.FromBytes(artBytes)
				if _, err := testAPIManifestPut(t, s, "referrer", artDig.String(), artBytes); err != nil {
					return
				}
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
				if len(found) != count {
					t.Errorf("length of descriptor list, expected %d, received %d", count, len(found))
				}
				// verify full list contains all count values (make a slice of bool, set to true as each one is found, verify full list is true)
				for i := 0; i < count; i++ {
					if !found[fmt.Sprintf("%d", i)] {
						t.Errorf("response not found for %d", i)
					}
				}
				if found[fmt.Sprintf("%d", count)] {
					t.Errorf("referrer pushed after first page returned should not have been included")
				}
				// query again to verify cache works
				_, err = testAPIReferrersList(t, s, "referrer", subDig, "", nil)
				if err != nil {
					return
				}
			})
			t.Run("digest-512", func(t *testing.T) {
				if tcServer.readOnly {
					return
				}
				t.Parallel()
				repo := "digest-512"
				tag := "sha512"
				// setup image content with one layer sha256, and rest of the content sha512
				layer1Blob := []byte("hello sha256")
				layer1Dig := digest.SHA256.FromBytes(layer1Blob)
				layer2Blob := []byte("hello sha512")
				layer2Dig := digest.SHA512.FromBytes(layer2Blob)
				configJSON := sampleImage{
					Platform: types.Platform{
						OS:           "linux",
						Architecture: "amd64",
					},
					Config: sampleConfig{
						Cmd: []string{"yolo"},
					},
					RootFS: sampleRootFS{
						Type: "layers",
						DiffIDs: []digest.Digest{
							layer1Dig, layer2Dig,
						},
					},
				}
				configBlob, err := json.Marshal(configJSON)
				if err != nil {
					t.Fatalf("failed to marshal config: %v", err)
				}
				configDig := digest.SHA512.FromBytes(configBlob)
				man := types.Manifest{
					SchemaVersion: 2,
					MediaType:     types.MediaTypeOCI1Manifest,
					Config: types.Descriptor{
						MediaType: types.MediaTypeOCI1ImageConfig,
						Digest:    configDig,
						Size:      int64(len(configBlob)),
					},
					Layers: []types.Descriptor{
						{
							MediaType: types.MediaTypeOCI1Layer,
							Size:      int64(len(layer1Blob)),
							Digest:    layer1Dig,
						},
						{
							MediaType: types.MediaTypeOCI1Layer,
							Size:      int64(len(layer2Blob)),
							Digest:    layer2Dig,
						},
					},
				}
				manBlob, err := json.Marshal(man)
				if err != nil {
					t.Fatalf("failed to marshal manifest: %v", err)
				}
				manDig := digest.SHA512.FromBytes(manBlob)
				ind := types.Index{
					SchemaVersion: 2,
					MediaType:     types.MediaTypeOCI1ManifestList,
					Manifests: []types.Descriptor{
						{
							MediaType: types.MediaTypeOCI1Manifest,
							Digest:    manDig,
							Size:      int64(len(manBlob)),
						},
					},
				}
				indBlob, err := json.Marshal(ind)
				if err != nil {
					t.Fatalf("failed to marshal index: %v", err)
				}
				indDig := digest.SHA512.FromBytes(indBlob)
				// push content
				// first sha256 blob is a standard push
				_, err = testAPIBlobPostPut(t, s, repo, layer1Dig, layer1Blob)
				if err != nil {
					t.Fatalf("failed to blob post: %v", err)
				}
				// second blob is a sha512 pushed with a POST/PATCH/PUT
				u, err := url.Parse("/v2/" + repo + "/blobs/uploads/")
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				q := u.Query()
				q.Add("digest-algorithm", layer2Dig.Algorithm().String())
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
				resp, err = testClientRun(t, s, "PATCH", u.String(), layer2Blob,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientReqHeader("Content-Length", fmt.Sprintf("%d", len(layer2Blob))),
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
				q.Add("digest", layer2Dig.String())
				u.RawQuery = q.Encode()
				_, err = testClientRun(t, s, "PUT", u.String(), nil,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientRespStatus(http.StatusCreated),
				)
				if err != nil {
					t.Fatalf("failed to send blob put: %v", err)
				}
				// config blob is pushed with a monolithic POST
				u, err = url.Parse("/v2/" + repo + "/blobs/uploads/")
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				q = u.Query()
				q.Add("digest", configDig.String())
				u.RawQuery = q.Encode()
				_, err = testClientRun(t, s, "POST", u.String(), configBlob,
					testClientReqHeader("Content-Type", "application/octet-stream"),
					testClientRespStatus(http.StatusCreated),
					testClientRespHeader("Location", ""))
				if err != nil {
					t.Fatalf("failed to run monolithic upload: %v", err)
				}
				// push manifest with sha512 by digest
				_, err = testAPIManifestPut(t, s, repo, manDig.String(), manBlob)
				if err != nil {
					t.Fatalf("failed to manifest put by digest: %v", err)
				}
				// push index by tag with sha512 digest arg
				u, err = url.Parse("/v2/" + repo + "/manifests/" + tag)
				if err != nil {
					t.Fatalf("failed to parse URL: %v", err)
				}
				q = u.Query()
				q.Set("digest", indDig.String())
				u.RawQuery = q.Encode()
				_, err = testClientRun(t, s, "PUT", u.String(), indBlob,
					testClientReqHeader("Content-Type", types.MediaTypeOCI1ManifestList),
					testClientRespStatus(http.StatusCreated),
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
				_, err = testAPIBlobHead(t, s, repo, layer2Dig)
				if err != nil {
					t.Errorf("failed to head layer2: %v", err)
				}
				_, err = testAPIBlobGet(t, s, repo, layer2Dig, layer2Blob)
				if err != nil {
					t.Errorf("failed to get layer2: %v", err)
				}
				_, err = testAPIBlobGet(t, s, repo, configDig, configBlob)
				if err != nil {
					t.Errorf("failed to get config: %v", err)
				}
				// delete blobs by digest
				_, err = testAPIBlobRm(t, s, repo, layer1Dig)
				if err != nil {
					t.Errorf("failed to delete layer1: %v", err)
				}
				_, err = testAPIBlobRm(t, s, repo, layer2Dig)
				if err != nil {
					t.Errorf("failed to delete layer2: %v", err)
				}
				_, err = testAPIBlobRm(t, s, repo, configDig)
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
				if tcServer.readOnly {
					return
				}
				t.Parallel()
				repo := "digest-unknown"
				sd, err := genSampleData(t)
				if err != nil {
					t.Fatalf("failed to generate sample data")
				}
				// create a digest with an unknown algorithm
				badDig := digest.Digest("unknown:12345")
				// attempt to get/head blob
				u, err := url.Parse("/v2/" + repo + "/blobs/" + badDig.String())
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
				u, err = url.Parse("/v2/" + repo + "/blobs/uploads/?digest=" + url.QueryEscape(badDig.String()))
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
				u, err = url.Parse("/v2/" + repo + "/manifests/" + badDig.String())
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
				u, err = url.Parse("/v2/" + repo + "/manifests/bad?digest=" + url.QueryEscape(badDig.String()))
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
			// TODO: test tag listing before and after pushing manifest
			// TODO: test blob chunked upload, and stream upload
			// TODO: test referrers pagination: push referrer with annotations too large to fit in the referrers response manifest, verify response does not include the referrer
		})
	}
}

func TestMatchV2(t *testing.T) {
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

type testClient func(req *http.Request) (*httptest.ResponseRecorder, error)
type testClientGen func(t *testing.T, next testClient) testClient

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
			found := false
			for _, c := range codes {
				if c == resp.Code {
					found = true
					break
				}
			}
			if !found {
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

func testClientRespHeader(k, v string) testClientGen {
	return func(t *testing.T, next testClient) testClient {
		return func(req *http.Request) (*httptest.ResponseRecorder, error) {
			t.Helper()
			resp, err := next(req)
			respV := resp.Header().Values(k)
			if v != "" {
				found := false
				for _, rv := range respV {
					if rv == v {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("header mismatch for %s, expected %s, received %v", k, v, respV)
					err = errValidationFailed
				}
			}
			// if value not set, just verify existence of header
			if v == "" && len(respV) == 0 {
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
	tcgList := []testClientGen{testClientRespStatus(http.StatusOK)}
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

type sampleData map[string]*sampleEntry
type sampleEntry struct {
	manifest     map[digest.Digest][]byte
	blob         map[digest.Digest][]byte
	manifestList []digest.Digest
}

const (
	layerPerImage      = 2
	layerPerReferrer   = 1
	layerSize          = 1024
	emptyJSON          = "{}"
	sampleArtifactType = "application/vnd.example.artifact+json"
)

var (
	imagePlatforms = []string{"amd64", "arm64"}
)

func genSampleData(t *testing.T) (sampleData, error) {
	now := time.Now()
	seed := now.UnixNano()
	t.Logf("random seed for sample data initialized to %d", seed)
	rng := rand.New(rand.NewSource(seed))
	sd := sampleData{}
	// setup example image manifests
	imageCount := len(imagePlatforms)
	images := make([]*sampleEntry, imageCount)
	for i := 0; i < imageCount; i++ {
		entry := sampleEntry{
			manifest:     map[digest.Digest][]byte{},
			blob:         map[digest.Digest][]byte{},
			manifestList: []digest.Digest{},
		}
		conf := sampleImage{
			Create: &now,
			Platform: types.Platform{
				OS:           "linux",
				Architecture: imagePlatforms[i],
			},
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
				DiffIDs: make([]digest.Digest, layerPerImage),
			},
		}
		man := types.Manifest{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1Manifest,
			Layers:        make([]types.Descriptor, layerPerImage),
		}
		for l := 0; l < layerPerImage; l++ {
			digOrig, digComp, bytesComp, err := genSampleLayer(rng, layerSize)
			if err != nil {
				return nil, err
			}
			entry.blob[digComp] = bytesComp
			conf.RootFS.DiffIDs[l] = digOrig
			man.Layers[l] = types.Descriptor{
				MediaType: types.MediaTypeOCI1LayerGzip,
				Size:      int64(layerSize),
				Digest:    digComp,
			}
		}
		confJSON, err := json.Marshal(conf)
		if err != nil {
			return nil, err
		}
		confDig := digest.Canonical.FromBytes(confJSON)
		entry.blob[confDig] = confJSON
		man.Config = types.Descriptor{
			MediaType: types.MediaTypeOCI1ImageConfig,
			Size:      int64(len(confJSON)),
			Digest:    confDig,
		}
		manJSON, err := json.Marshal(man)
		if err != nil {
			return nil, err
		}
		manDig := digest.Canonical.FromBytes(manJSON)
		entry.manifest[manDig] = manJSON
		entry.manifestList = append(entry.manifestList, manDig)
		images[i] = &entry
		sd[fmt.Sprintf("image-%s", imagePlatforms[i])] = &entry
	}
	// TODO: setup an image with a foreign layer

	// setup example index
	ind := types.Index{
		SchemaVersion: 2,
		MediaType:     types.MediaTypeOCI1ManifestList,
		Manifests:     make([]types.Descriptor, imageCount),
		Annotations:   map[string]string{},
	}
	for l := 0; l < imageCount; l++ {
		ind.Manifests[l] = types.Descriptor{
			MediaType: types.MediaTypeOCI1Manifest,
			Size:      int64(len(images[l].manifest[images[l].manifestList[0]])),
			Digest:    images[l].manifestList[0],
			Platform: &types.Platform{
				OS:           "linux",
				Architecture: imagePlatforms[l],
			},
		}
	}
	ind.Annotations["config-type"] = "test"
	indJSON, err := json.Marshal(ind)
	if err != nil {
		return nil, err
	}
	indDig := digest.Canonical.FromBytes(indJSON)
	indEntry := sampleEntry{
		blob: map[digest.Digest][]byte{},
		manifest: map[digest.Digest][]byte{
			indDig: indJSON,
		},
		manifestList: []digest.Digest{},
	}
	// merge images into index entry
	for i := 0; i < imageCount; i++ {
		for k, b := range images[i].blob {
			indEntry.blob[k] = b
		}
		for k, m := range images[i].manifest {
			indEntry.manifest[k] = m
		}
		indEntry.manifestList = append(indEntry.manifestList, images[i].manifestList...)
	}
	indEntry.manifestList = append(indEntry.manifestList, indDig)
	sd["index"] = &indEntry

	// TODO: setup example referrers: config with content, empty config, annotation only

	return sd, nil
}

func genSampleLayer(rng *rand.Rand, size int) (digest.Digest, digest.Digest, []byte, error) {
	layerOrigBytes := make([]byte, size)
	if _, err := rng.Read(layerOrigBytes); err != nil {
		return "", "", []byte{}, err
	}
	var layerCompBuf bytes.Buffer
	gzW := gzip.NewWriter(&layerCompBuf)
	if _, err := gzW.Write(layerOrigBytes); err != nil {
		return "", "", []byte{}, err
	}
	if err := gzW.Close(); err != nil {
		return "", "", []byte{}, err
	}
	layerCompBytes := layerCompBuf.Bytes()
	digOrig := digest.Canonical.FromBytes(layerOrigBytes)
	digComp := digest.Canonical.FromBytes(layerCompBytes)
	return digOrig, digComp, layerCompBytes, nil
}

// sample types are partial schemas for image configs
type sampleImage struct {
	Create *time.Time `json:"created,omitempty"`
	types.Platform
	Config sampleConfig `json:"config,omitempty"`
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
