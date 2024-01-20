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
	existingRepo := "testrepo"
	existingTag := "v2"
	tempDir := t.TempDir()
	err = copy.Copy(tempDir+"/"+existingRepo, "./testdata/"+existingRepo)
	if err != nil {
		t.Errorf("failed to copy %s to tempDir: %v", existingRepo, err)
		return
	}
	boolT := true
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
				},
			},
		},
		{
			name: "Mem with Dir",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					RootDir:   "./testdata",
				},
			},
			existing: true,
		},
		{
			name: "Dir",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   tempDir,
					GC: config.ConfigGC{
						Frequency:   time.Second * -1,
						GracePeriod: time.Second * -1,
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
				digI, err := digest.Parse(resp.Header().Get("Docker-Content-Digest"))
				if err != nil {
					t.Errorf("unable to parse index digest, %s: %v", resp.Header().Get("Docker-Content-Digest"), err)
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
				resp, err = testAPIReferrersList(t, s, existingRepo, digI, nil)
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
				if rr.MediaType != types.MediaTypeOCI1ManifestList || rr.SchemaVersion != 2 || len(rr.Manifests) < 2 {
					t.Errorf("referrers response should be an index, schema 2, and at least 2 manifests: %v", rr)
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
				if !tcServer.testGC {
					return
				}
				// this test is not parallel because the server is closed and restarted
				// push two images
				if err := testSampleEntryPush(t, s, *sd["image-amd64"], "gc", "amd64"); err != nil {
					return
				}
				if err := testSampleEntryPush(t, s, *sd["image-arm64"], "gc", "arm64"); err != nil {
					return
				}
				// delete one manifest
				if _, err := testAPIManifestRm(t, s, "gc", sd["image-arm64"].manifestList[0].String()); err != nil {
					t.Fatalf("failed to remove manifest: %v", err)
				}
				// close server and recreate
				if err := s.Close(); err != nil {
					t.Errorf("failed to close server: %v", err)
				}
				s = New(tcServer.conf)
				// verify get
				if err := testSampleEntryPull(t, s, *sd["image-amd64"], "gc", "amd64"); err != nil {
					t.Errorf("failed to pull entry after recreating server: %v", err)
				}
				// verify GC
				for dig := range sd["image-arm64"].blob {
					if _, ok := sd["image-amd64"].blob[dig]; ok {
						continue // skip dup blobs
					}
					_, err := testClientRun(t, s, "GET", "/v2/gc/blobs/"+dig.String(), nil,
						testClientRespStatus(http.StatusNotFound))
					if err != nil {
						t.Errorf("did not receive a not-found error on a GC blob: %v", err)
					}
				}
			})
			// TODO: test tag listing before and after pushing manifest
			// TODO: test deleting manifests and blobs
			// TODO: test blob chunked upload, monolithic upload, and stream upload
			// TODO: test pushing manifest with subject and querying referrers
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

func testAPIReferrersList(t *testing.T, s *Server, repo string, dig digest.Digest, body []byte) (*httptest.ResponseRecorder, error) {
	t.Helper()
	tcgList := []testClientGen{
		testClientRespStatus(http.StatusOK),
		testClientRespHeader("Content-Type", types.MediaTypeOCI1ManifestList),
	}
	if body != nil {
		tcgList = append(tcgList, testClientRespBody(body))
	}
	resp, err := testClientRun(t, s, "GET", "/v2/"+repo+"/referrers/"+dig.String(), nil, tcgList...)
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
