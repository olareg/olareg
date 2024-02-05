package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/copy"
	"github.com/olareg/olareg/types"
)

// verify interface implementation
var (
	_ Store = &dir{}
	_ Repo  = &dirRepo{}
	_ Store = &mem{}
	_ Repo  = &memRepo{}
)

func TestStore(t *testing.T) {
	t.Parallel()
	existingRepo := "testrepo"
	existingTag := "v1"
	newRepo := "new-repo"
	newBlobRaw := []byte("{}")
	newBlobDigest := digest.Canonical.FromBytes(newBlobRaw)
	newManifest := types.Manifest{
		SchemaVersion: 2,
		MediaType:     types.MediaTypeOCI1Manifest,
		ArtifactType:  "application/vnd.example.test",
		Config: types.Descriptor{
			MediaType: types.MediaTypeOCI1Empty,
			Digest:    newBlobDigest,
			Size:      int64(len(newBlobRaw)),
		},
		Layers: []types.Descriptor{
			{
				MediaType: types.MediaTypeOCI1Empty,
				Digest:    newBlobDigest,
				Size:      int64(len(newBlobRaw)),
			},
		},
		Annotations: map[string]string{
			"test": "empty manifest for quick test",
		},
	}
	newManifestRaw, err := json.Marshal(newManifest)
	if err != nil {
		t.Errorf("failed to marshal manifest: %v", err)
		return
	}
	newManifestDigest := digest.Canonical.FromBytes(newManifestRaw)
	newTag := "artifact"
	tempDir := t.TempDir()
	err = copy.Copy(tempDir+"/"+existingRepo, "../../testdata/"+existingRepo)
	if err != nil {
		t.Errorf("failed to copy %s to tempDir: %v", existingRepo, err)
		return
	}
	tt := []struct {
		name         string
		conf         config.Config
		testExisting bool
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
					RootDir:   "../../testdata",
				},
			},
			testExisting: true,
		},
		{
			name: "Dir",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   tempDir,
				},
			},
			testExisting: true,
		},
	}
	for _, tc := range tt {
		tc := tc
		tc.conf.SetDefaults()
		var s Store
		switch tc.conf.Storage.StoreType {
		case config.StoreDir:
			s = NewDir(tc.conf)
		case config.StoreMem:
			s = NewMem(tc.conf)
		default:
			t.Errorf("unsupported store type: %d", tc.conf.Storage.StoreType)
			return
		}
		t.Cleanup(func() { _ = s.Close() })
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Run("Restart", func(t *testing.T) {
				// not parallel since store is recreated
				repo, err := s.RepoGet(newRepo)
				if err != nil {
					t.Fatalf("failed to create repo: %v", err)
				}
				// begin several blob uploads, but do not complete
				_, _, err = repo.BlobCreate()
				if err != nil {
					t.Fatalf("failed to create blob: %v", err)
				}
				_, _, err = repo.BlobCreate()
				if err != nil {
					t.Fatalf("failed to create blob: %v", err)
				}
				bc, _, err := repo.BlobCreate()
				if err != nil {
					t.Fatalf("failed to create blob: %v", err)
				}
				_, err = bc.Write([]byte(`hello world`))
				if err != nil {
					t.Fatalf("failed to write blob: %v", err)
				}
				repo.Done()
				// close and recreate store
				err = s.Close()
				if err != nil {
					t.Errorf("failed to close store: %v", err)
				}
				switch tc.conf.Storage.StoreType {
				case config.StoreDir:
					s = NewDir(tc.conf)
				case config.StoreMem:
					s = NewMem(tc.conf)
				}
			})
			t.Run("Existing", func(t *testing.T) {
				if !tc.testExisting {
					t.Skip("No existing repo to test")
				}
				t.Parallel()
				// query testrepo content
				repo, err := s.RepoGet(existingRepo)
				if err != nil {
					t.Errorf("failed to get repo %s: %v", existingRepo, err)
					return
				}
				defer repo.Done()
				// get the index
				i, err := repo.IndexGet()
				if err != nil {
					t.Errorf("failed to get index: %v", err)
				}
				desc, err := i.GetDesc(existingTag)
				if err != nil {
					t.Errorf("failed to get tag %s: %v", existingTag, err)
					return
				}
				// get a manifest
				rdr, err := repo.BlobGet(desc.Digest)
				if err != nil {
					t.Errorf("failed to get manifest: %v", err)
					return
				}
				blobRaw, err := io.ReadAll(rdr)
				if err != nil {
					t.Errorf("failed to read blob: %v", err)
					return
				}
				err = rdr.Close()
				if err != nil {
					t.Errorf("failed to close blob reader: %v", err)
				}
				m, err := repo.blobMeta(desc.Digest, false)
				if err != nil {
					t.Errorf("failed to get metadata on blob")
				} else if m.mod.Equal(time.Time{}) {
					t.Errorf("metadata mod time is not set")
				}
				// delete a manifest
				err = repo.BlobDelete(desc.Digest)
				if err != nil {
					t.Errorf("failed to delete blob: %v", err)
				}
				// verify delete
				rdr, err = repo.BlobGet(desc.Digest)
				if err == nil {
					t.Errorf("blob get succeeded after delete")
					_ = rdr.Close()
				}
				_, err = repo.blobMeta(desc.Digest, false)
				if err == nil {
					t.Errorf("get metadata on deleted blob did not fail")
				}
				// delete index entry
				err = repo.IndexRemove(desc)
				if err != nil {
					t.Errorf("IndexRm failed: %v", err)
				}
				i, err = repo.IndexGet()
				if err != nil {
					t.Errorf("failed to get index: %v", err)
				}
				_, err = i.GetDesc(existingTag)
				if err == nil {
					t.Errorf("tag found after remove")
				}
				// push again
				bc, _, err := repo.BlobCreate(BlobWithDigest(desc.Digest))
				if err != nil {
					t.Errorf("failed to create new blob: %v", err)
					return
				}
				_, err = bc.Write(blobRaw)
				if err != nil {
					t.Errorf("failed to write blob: %v", err)
				}
				err = bc.Close()
				if err != nil {
					t.Errorf("failed to close blob: %v", err)
				}
				// verity exists
				rdr, err = repo.BlobGet(desc.Digest)
				if err != nil {
					t.Errorf("blob get succeeded failed after create: %v", err)
				}
				err = rdr.Close()
				if err != nil {
					t.Errorf("blob close failed after create: %v", err)
				}
				m, err = repo.blobMeta(desc.Digest, false)
				if err != nil {
					t.Errorf("failed to get metadata on recreated blob")
				} else if m.mod.Equal(time.Time{}) {
					t.Errorf("metadata mod time is not set")
				}
				// add index entry
				desc.Annotations = map[string]string{
					types.AnnotRefName: existingTag,
				}
				err = repo.IndexInsert(desc)
				if err != nil {
					t.Errorf("failed to add index entry: %v", err)
				}
				// verify index entry exists
				i, err = repo.IndexGet()
				if err != nil {
					t.Errorf("failed to get index after add: %v", err)
				}
				desc, err = i.GetDesc(existingTag)
				if err != nil {
					t.Errorf("failed to get tag %s after add: %v", existingTag, err)
				}
			})
			t.Run("New", func(t *testing.T) {
				t.Parallel()
				// subtract a second to deal with race conditions in the time granularity from directory storage
				start := time.Now().Add(time.Second * -1)
				// get new repo
				repo, err := s.RepoGet(newRepo)
				if err != nil {
					t.Errorf("failed to get repo %s: %v", newRepo, err)
					return
				}
				defer repo.Done()
				// get index
				i, err := repo.IndexGet()
				if err != nil {
					t.Errorf("failed to get index: %v", err)
				}
				if len(i.Manifests) > 0 {
					t.Errorf("new repo contains manifests")
				}
				// get blob
				rdr, err := repo.BlobGet(newBlobDigest)
				if err == nil {
					t.Errorf("blob get on empty repo did not fail")
					_ = rdr.Close()
				}
				_, err = repo.blobMeta(newBlobDigest, false)
				if err == nil {
					t.Errorf("blobMeta on empty repo did not fail")
				}
				// push blobs
				bc, session1, err := repo.BlobCreate(BlobWithDigest(newBlobDigest))
				if err != nil {
					t.Errorf("failed to create new blob: %v", err)
				}
				_, err = bc.Write(newBlobRaw)
				if err != nil {
					t.Errorf("failed to write new blob: %v", err)
				}
				err = bc.Close()
				if err != nil {
					t.Errorf("failed to close new blob: %v", err)
				}
				err = bc.Verify(newBlobDigest)
				if err != nil {
					t.Errorf("failed to verify new blob: %v", err)
				}
				err = bc.Verify(newManifestDigest)
				if err == nil {
					t.Errorf("blob did not fail when verifying with manifest digest")
				}
				_, err = repo.blobMeta(newManifestDigest, false)
				if err == nil {
					t.Errorf("blobMeta on manifest after pushing blob did not fail")
				}
				_, session2, err := repo.BlobCreate()
				if err != nil {
					t.Errorf("failed to create new manifest: %v", err)
				}
				bc, err = repo.BlobSession(session2)
				if err != nil {
					t.Errorf("failed to get session: %v", err)
				}
				_, err = bc.Write(newManifestRaw)
				if err != nil {
					t.Errorf("failed to write new manifest: %v", err)
				}
				err = bc.Close()
				if err != nil {
					t.Errorf("failed to close new manifest: %v", err)
				}
				err = bc.Verify(newManifestDigest)
				if err != nil {
					t.Errorf("failed to verify new manifest: %v", err)
				}
				bc, session3, err := repo.BlobCreate()
				if err != nil {
					t.Errorf("failed to create new blob: %v", err)
				}
				bc.Cancel()
				// verify closed and canceled sessions are no longer available
				for i, sessionID := range []string{session1, session2, session3} {
					_, err := repo.BlobSession(sessionID)
					if err == nil {
						t.Errorf("session %d was returned after close/cancel", i)
					}
				}
				// get blobs
				rdr, err = repo.BlobGet(newBlobDigest)
				if err != nil {
					t.Errorf("failed to get blob: %v", err)
				}
				b, err := io.ReadAll(rdr)
				if err != nil {
					t.Errorf("failed to read blob: %v", err)
				}
				if !bytes.Equal(b, newBlobRaw) {
					t.Errorf("blob mismatch, expected %s, received %s", string(newBlobRaw), string(b))
				}
				err = rdr.Close()
				if err != nil {
					t.Errorf("failed to close blob: %v", err)
				}
				m, err := repo.blobMeta(newBlobDigest, false)
				if err != nil {
					t.Errorf("failed to get metadata on new blob: %v", err)
				}
				if m.mod.Before(start) {
					t.Errorf("new blob mod time is before test start (%s < %s)", m.mod.String(), start.String())
				}
				rdr, err = repo.BlobGet(newManifestDigest)
				if err != nil {
					t.Errorf("failed to get manifest: %v", err)
				}
				b, err = io.ReadAll(rdr)
				if err != nil {
					t.Errorf("failed to read manifest: %v", err)
				}
				if !bytes.Equal(b, newManifestRaw) {
					t.Errorf("manifest mismatch, expected %s, received %s", string(newManifestRaw), string(b))
				}
				err = rdr.Close()
				if err != nil {
					t.Errorf("failed to close manifest: %v", err)
				}
				m, err = repo.blobMeta(newManifestDigest, false)
				if err != nil {
					t.Errorf("failed to get metadata on new manifest: %v", err)
				}
				if m.mod.Before(start) {
					t.Errorf("new manifest mod time is before test start (%s < %s)", m.mod.String(), start.String())
				}
				// add index entry
				newDesc := types.Descriptor{
					MediaType: types.MediaTypeOCI1Manifest,
					Digest:    newManifestDigest,
					Size:      int64(len(newManifestRaw)),
					Annotations: map[string]string{
						types.AnnotRefName: newTag,
					},
				}
				err = repo.IndexInsert(newDesc)
				if err != nil {
					t.Errorf("failed to insert descriptor: %v", err)
				}
				i, err = repo.IndexGet()
				if err != nil {
					t.Errorf("failed to get index: %v", err)
				}
				desc, err := i.GetDesc(newTag)
				if err != nil {
					t.Errorf("failed to get newly added tag: %v", err)
				}
				if desc.Digest != newManifestDigest {
					t.Errorf("returned descriptor mismatch: expect %s, received %s", desc.Digest.String(), newManifestDigest.String())
				}
				// rm blobs
				err = repo.BlobDelete(newBlobDigest)
				if err != nil {
					t.Errorf("failed to delete blob: %v", err)
				}
				err = repo.BlobDelete(newManifestDigest)
				if err != nil {
					t.Errorf("failed to delete manifest: %v", err)
				}
				// get blobs
				rdr, err = repo.BlobGet(newBlobDigest)
				if err == nil {
					t.Errorf("blob get succeeded after delete")
					_ = rdr.Close()
				}
				rdr, err = repo.BlobGet(newManifestDigest)
				if err == nil {
					t.Errorf("manifest get succeeded after delete")
					_ = rdr.Close()
				}
				// rm tag
				err = repo.IndexRemove(newDesc)
				if err != nil {
					t.Errorf("failed to delete index entry: %v", err)
				}
				i, err = repo.IndexGet()
				if err != nil {
					t.Errorf("failed to get index: %v", err)
				}
				_, err = i.GetDesc(newTag)
				if err == nil {
					t.Errorf("descriptor found on deleted tag")
				}
				desc, err = i.GetDesc(newManifestDigest.String())
				if err != nil {
					t.Errorf("failed to find untagged manifest: %v", err)
				}
				// rm digest
				err = repo.IndexRemove(desc)
				if err != nil {
					t.Errorf("failed to delete index entry: %v", err)
				}
				i, err = repo.IndexGet()
				if err != nil {
					t.Errorf("failed to get index: %v", err)
				}
				_, err = i.GetDesc(newTag)
				if err == nil {
					t.Errorf("descriptor found on deleted tag")
				}
				_, err = i.GetDesc(newManifestDigest.String())
				if err == nil {
					t.Errorf("descriptor found on deleted digest")
				}
				if len(i.Manifests) != 0 {
					t.Errorf("entries found in empty index: %v", i)
				}
			})
		})
	}
	// TODO: add concurrency tests, multiple uploads, multiple gets
}

func TestGarbageCollect(t *testing.T) {
	var err error
	// TODO: track description of each blob for error messages
	testRepo := "testrepo"
	// create test data to push
	blobList := [][]byte{}
	descList := []types.Descriptor{}
	childList := []types.Descriptor{}
	// - dangling blob
	dataBlob := []byte("dangling blob")
	digBlob := digest.Canonical.FromBytes(dataBlob)
	blobList = append(blobList, dataBlob)
	// - images
	dataImageConf := []byte(`{}`)
	digImageConf := digest.Canonical.FromBytes(dataImageConf)
	blobList = append(blobList, dataImageConf)
	imageCount := 4 // 2 index entries, tagged, and untagged
	const (
		imageChild1   = 0
		imageChild2   = 1
		imageTagged   = 2
		imageUntagged = 3
	)
	dataImageLayer := make([][]byte, imageCount)
	digImageLayer := make([]digest.Digest, imageCount)
	dataImage := make([][]byte, imageCount)
	digImage := make([]digest.Digest, imageCount)
	for i := 0; i < imageCount; i++ {
		dataImageLayer[i] = []byte(fmt.Sprintf("layer for image %d", i))
		digImageLayer[i] = digest.Canonical.FromBytes(dataImageLayer[i])
		dataImageMan := types.Manifest{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1Manifest,
			Config: types.Descriptor{
				MediaType: types.MediaTypeOCI1ImageConfig,
				Digest:    digImageConf,
				Size:      int64(len(dataImageConf)),
			},
			Layers: []types.Descriptor{
				{
					MediaType: types.MediaTypeOCI1LayerGzip,
					Digest:    digImageLayer[i],
					Size:      int64(len(dataImageLayer[i])),
				},
			},
		}
		dataImage[i], err = json.Marshal(dataImageMan)
		if err != nil {
			t.Fatalf("failed to marshal image manifest: %v", err)
		}
		digImage[i] = digest.Canonical.FromBytes(dataImage[i])
	}
	blobList = append(blobList, dataImageLayer...)
	blobList = append(blobList, dataImage...)
	childList = append(childList,
		types.Descriptor{
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digImage[imageChild1],
			Size:      int64(len(dataImage[imageChild1])),
		},
		types.Descriptor{
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digImage[imageChild2],
			Size:      int64(len(dataImage[imageChild2])),
		},
	)
	descList = append(descList,
		types.Descriptor{
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digImage[imageTagged],
			Size:      int64(len(dataImage[imageTagged])),
			Annotations: map[string]string{
				types.AnnotRefName: "image-2",
			},
		},
		types.Descriptor{
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digImage[imageUntagged],
			Size:      int64(len(dataImage[imageUntagged])),
		},
	)
	// - index of first two images
	dataIndexMan := types.Index{
		SchemaVersion: 2,
		MediaType:     types.MediaTypeOCI1ManifestList,
		Manifests: []types.Descriptor{
			{
				MediaType: types.MediaTypeOCI1Manifest,
				Digest:    digImage[0],
				Size:      int64(len(dataImage[0])),
			},
			{
				MediaType: types.MediaTypeOCI1Manifest,
				Digest:    digImage[1],
				Size:      int64(len(dataImage[1])),
			},
		},
	}
	dataIndex, err := json.Marshal(dataIndexMan)
	if err != nil {
		t.Fatalf("failed to marshal index manifest: %v", err)
	}
	digIndex := digest.Canonical.FromBytes(dataIndex)
	blobList = append(blobList, dataIndex)
	descList = append(descList,
		types.Descriptor{
			MediaType: types.MediaTypeOCI1ManifestList,
			Digest:    digIndex,
			Size:      int64(len(dataIndex)),
			Annotations: map[string]string{
				types.AnnotRefName: "index-0",
			},
		},
	)
	// - referrers to various digests
	digUnknown := digest.Canonical.FromString("unknown manifest digest")
	artifactType := "application/vnd.example.test"
	subjectList := map[digest.Digest][]types.Descriptor{}
	referrerCount := 7
	const (
		referrerToChild1   = 0
		referrerToChild2   = 1
		referrerToTagged   = 2
		referrerToUntagged = 3
		referrerToIndex    = 4
		referrerToDangling = 5
		referrerToPruned   = 6
	)
	digReferrerLayer := make([]digest.Digest, referrerCount)
	digReferrer := make([]digest.Digest, referrerCount)
	subjReferrer := []types.Descriptor{
		referrerToChild1: {
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digImage[imageChild1],
			Size:      int64(len(dataImage[imageChild1])),
		},
		referrerToChild2: {
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digImage[imageChild2],
			Size:      int64(len(dataImage[imageChild2])),
		},
		referrerToTagged: {
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digImage[imageTagged],
			Size:      int64(len(dataImage[imageTagged])),
		},
		referrerToUntagged: {
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digImage[imageUntagged],
			Size:      int64(len(dataImage[imageUntagged])),
		},
		referrerToIndex: {
			MediaType: types.MediaTypeOCI1ManifestList,
			Digest:    digIndex,
			Size:      int64(len(dataIndex)),
		},
		referrerToDangling: {
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digUnknown,
			Size:      42,
		},
		referrerToPruned: {
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    digBlob,
			Size:      int64(len(dataBlob)),
		},
	}
	for i, subj := range subjReferrer {
		dataLayer := []byte(fmt.Sprintf("layer for referrer %d", i))
		digLayer := digest.Canonical.FromBytes(dataLayer)
		dataMan := types.Manifest{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1Manifest,
			ArtifactType:  artifactType,
			Config: types.Descriptor{
				MediaType: types.MediaTypeOCI1ImageConfig,
				Digest:    digImageConf,
				Size:      int64(len(dataImageConf)),
			},
			Layers: []types.Descriptor{
				{
					MediaType: types.MediaTypeOCI1LayerGzip,
					Digest:    digLayer,
					Size:      int64(len(dataLayer)),
				},
			},
			Subject: &subj,
		}
		data, err := json.Marshal(dataMan)
		if err != nil {
			t.Fatalf("failed to marshal referrer: %v", err)
		}
		dig := digest.Canonical.FromBytes(data)
		digReferrerLayer[i] = digLayer
		digReferrer[i] = dig
		blobList = append(blobList, dataLayer, data)
		childList = append(childList, types.Descriptor{
			MediaType: types.MediaTypeOCI1Manifest,
			Digest:    dig,
			Size:      int64(len(data)),
		})
		subjectList[subj.Digest] = append(subjectList[subj.Digest], types.Descriptor{
			MediaType:    types.MediaTypeOCI1Manifest,
			Digest:       dig,
			Size:         int64(len(data)),
			ArtifactType: artifactType,
		})
	}
	// - circular index / referrer
	dataCircularMan := types.Index{
		SchemaVersion: 2,
		MediaType:     types.MediaTypeOCI1ManifestList,
		ArtifactType:  artifactType,
		Manifests: []types.Descriptor{
			{
				MediaType: types.MediaTypeOCI1ManifestList,
				Digest:    digIndex,
				Size:      int64(len(dataIndex)),
			},
		},
		Subject: &types.Descriptor{
			MediaType: types.MediaTypeOCI1ManifestList,
			Digest:    digIndex,
			Size:      int64(len(dataIndex)),
		},
	}
	dataCircular, err := json.Marshal(dataCircularMan)
	if err != nil {
		t.Fatalf("failed to marshal index manifest: %v", err)
	}
	digCircular := digest.Canonical.FromBytes(dataCircular)
	blobList = append(blobList, dataCircular)
	childList = append(childList, types.Descriptor{
		MediaType: types.MediaTypeOCI1ManifestList,
		Digest:    digCircular,
		Size:      int64(len(dataCircular)),
	})
	subjectList[digIndex] = append(subjectList[digIndex], types.Descriptor{
		MediaType:    types.MediaTypeOCI1ManifestList,
		Digest:       digCircular,
		Size:         int64(len(dataCircular)),
		ArtifactType: artifactType,
	})
	// referrer responses
	for subj, ml := range subjectList {
		respMan := types.Index{
			SchemaVersion: 2,
			MediaType:     types.MediaTypeOCI1ManifestList,
			Manifests:     ml,
		}
		resp, err := json.Marshal(respMan)
		if err != nil {
			t.Fatalf("failed to marshal referrer response: %v", err)
		}
		dig := digest.Canonical.FromBytes(resp)
		blobList = append(blobList, resp)
		descList = append(descList, types.Descriptor{
			MediaType: types.MediaTypeOCI1ManifestList,
			Digest:    dig,
			Size:      int64(len(resp)),
			Annotations: map[string]string{
				types.AnnotReferrerSubject: subj.String(),
			},
		})
	}
	// create stores, with different GC policies, and expected blobs to exist or be deleted
	tempDir := t.TempDir()
	boolT := true
	boolF := false
	tt := []struct {
		name        string
		conf        config.Config
		expectExist []digest.Digest
		expectMiss  []digest.Digest
	}{
		{
			name: "Mem Untagged Dangling",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolF,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolF,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
			},
		},
		{
			name: "Mem Untagged Dangling with Subj",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolF,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
		},
		{
			name: "Mem Tagged Dangling with Subj",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolT,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
		},
		{
			name: "Mem Tagged with Subj",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolT,
						ReferrersDangling: &boolT,
						ReferrersWithSubj: &boolF,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
		},
		{
			name: "Mem Tagged",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolT,
						ReferrersDangling: &boolT,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
		},
		{
			name: "Mem Grace Period",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Hour,
						Untagged:          &boolT,
						ReferrersDangling: &boolT,
						ReferrersWithSubj: &boolF,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digCircular,
				digBlob,
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
			expectMiss: []digest.Digest{},
		},
		{
			name: "Mem Path Untagged Dangling",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					RootDir:   "../../testdata/",
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolF,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolF,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
			},
		},
		{
			name: "Dir Untagged Dangling",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   filepath.Join(tempDir, "untagged-dangling"),
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolF,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolF,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
			},
		},
		{
			name: "Dir Untagged Dangling with Subj",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   filepath.Join(tempDir, "untagged-dangling-subj"),
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolF,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
		},
		{
			name: "Dir Tagged Dangling with Subj",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   filepath.Join(tempDir, "tagged-dangling-subj"),
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolT,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
		},
		{
			name: "Dir Tagged with Subj",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   filepath.Join(tempDir, "tagged-subj"),
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolT,
						ReferrersDangling: &boolT,
						ReferrersWithSubj: &boolF,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
		},
		{
			name: "Dir Tagged",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   filepath.Join(tempDir, "tagged"),
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolT,
						ReferrersDangling: &boolT,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digCircular,
			},
			expectMiss: []digest.Digest{
				digBlob,
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
		},
		{
			name: "Dir Grace Period",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   filepath.Join(tempDir, "grace-period"),
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Hour,
						Untagged:          &boolT,
						ReferrersDangling: &boolT,
						ReferrersWithSubj: &boolF,
					},
				},
			},
			expectExist: []digest.Digest{
				digImageConf,
				digIndex,
				digImage[imageChild1], digImageLayer[imageChild1],
				digImage[imageChild2], digImageLayer[imageChild2],
				digImage[imageTagged], digImageLayer[imageTagged],
				digReferrer[referrerToChild1], digReferrerLayer[referrerToChild1],
				digReferrer[referrerToChild2], digReferrerLayer[referrerToChild2],
				digReferrer[referrerToTagged], digReferrerLayer[referrerToTagged],
				digReferrer[referrerToIndex], digReferrerLayer[referrerToIndex],
				digCircular,
				digBlob,
				digImage[imageUntagged], digImageLayer[imageUntagged],
				digReferrer[referrerToUntagged], digReferrerLayer[referrerToUntagged],
				digReferrer[referrerToDangling], digReferrerLayer[referrerToDangling],
				digReferrer[referrerToPruned], digReferrerLayer[referrerToPruned],
			},
			expectMiss: []digest.Digest{},
		},
		// TODO: add more tests with other GC conf settings
	}
	for _, tc := range tt {
		tc := tc
		tc.conf.SetDefaults()
		var s Store
		switch tc.conf.Storage.StoreType {
		case config.StoreDir:
			s = NewDir(tc.conf)
		case config.StoreMem:
			s = NewMem(tc.conf)
		default:
			t.Errorf("unsupported store type: %d", tc.conf.Storage.StoreType)
			return
		}
		t.Cleanup(func() { _ = s.Close() })
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, err := s.RepoGet(testRepo)
			if err != nil {
				t.Fatalf("failed to get repo: %v", err)
			}
			defer repo.Done()
			// push sample data
			for _, blob := range blobList {
				bc, _, err := repo.BlobCreate()
				if err != nil {
					t.Fatalf("failed to create blob: %v", err)
				}
				_, err = bc.Write(blob)
				if err != nil {
					t.Fatalf("failed to write blob: %v", err)
				}
				err = bc.Close()
				if err != nil {
					t.Fatalf("failed to close blob: %v", err)
				}
			}
			for i, desc := range descList {
				opts := []types.IndexOpt{}
				if i == len(descList)-1 {
					// append all children on last entry
					opts = append(opts, types.IndexWithChildren(childList))
				}
				err = repo.IndexInsert(desc, opts...)
				if err != nil {
					t.Fatalf("failed to insert descriptor: %v", err)
				}
			}
			// run gc
			err = repo.gc()
			if err != nil {
				t.Fatalf("failed to run GC: %v", err)
			}
			// check for blobs in expect lists
			for _, dig := range tc.expectExist {
				br, err := repo.BlobGet(dig)
				if err != nil {
					t.Errorf("failed to get expected blob %s: %v", dig.String(), err)
				} else {
					_ = br.Close()
				}
			}
			for _, dig := range tc.expectMiss {
				br, err := repo.BlobGet(dig)
				if err == nil {
					t.Errorf("received unexpected blob %s", dig.String())
					_ = br.Close()
				}
			}
		})
	}
}

func TestGarbageCollectUpload(t *testing.T) {
	t.Parallel()
	grace := time.Millisecond * 20
	freq := time.Millisecond * 10
	sleep := (freq * 2) + grace + (time.Millisecond * 50)
	retry := 100
	testRepo := "test"
	boolT := true
	boolF := false
	tempDir := t.TempDir()
	tt := []struct {
		name     string
		conf     config.Config
		expectGC bool
	}{
		{
			name: "Mem No GC",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolF,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectGC: false,
		},
		{
			name: "Mem GC",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Frequency:         freq,
						GracePeriod:       grace,
						Untagged:          &boolF,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectGC: true,
		},
		{
			name: "Dir No GC",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   filepath.Join(tempDir, "no-gc"),
					GC: config.ConfigGC{
						Frequency:         time.Second * -1,
						GracePeriod:       time.Second * -1,
						Untagged:          &boolF,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectGC: false,
		},
		{
			name: "Dir GC",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   filepath.Join(tempDir, "gc"),
					GC: config.ConfigGC{
						Frequency:         freq,
						GracePeriod:       grace,
						Untagged:          &boolF,
						ReferrersDangling: &boolF,
						ReferrersWithSubj: &boolT,
					},
				},
			},
			expectGC: true,
		},
	}
	for _, tc := range tt {
		tc := tc
		tc.conf.SetDefaults()
		var s Store
		switch tc.conf.Storage.StoreType {
		case config.StoreDir:
			s = NewDir(tc.conf)
		case config.StoreMem:
			s = NewMem(tc.conf)
		default:
			t.Errorf("unsupported store type: %d", tc.conf.Storage.StoreType)
			return
		}
		t.Cleanup(func() { _ = s.Close() })
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, err := s.RepoGet(testRepo)
			if err != nil {
				t.Fatalf("failed to get repo: %v", err)
			}
			defer repo.Done()
			// start upload
			_, sessionID, err := repo.BlobCreate()
			if err != nil {
				t.Fatalf("failed to create blob")
			}
			// sleep, releasing repo during sleep for GC
			time.Sleep(sleep)
			if tc.expectGC {
				success := false
				for i := 0; i < retry && !success; i++ {
					_, err = repo.BlobSession(sessionID)
					if err != nil {
						success = true
					} else {
						time.Sleep(sleep)
					}
				}
				if !success {
					// NOTE: this test is implicitly racy. Retry count and sleep time may need to be increased if it fails.
					t.Errorf("expected GC, upload session available")
				}
			} else {
				_, err = repo.BlobSession(sessionID)
				if err != nil {
					t.Errorf("did not expect GC, upload session destroyed")
				}
			}
		})
	}
}
