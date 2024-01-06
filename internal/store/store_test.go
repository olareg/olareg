package store

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/opencontainers/go-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/copy"
	"github.com/olareg/olareg/types"
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
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
				bc, err := repo.BlobCreate(BlobWithDigest(desc.Digest))
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
				// get new repo
				repo, err := s.RepoGet(newRepo)
				if err != nil {
					t.Errorf("failed to get repo %s: %v", newRepo, err)
					return
				}
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
				// push blobs
				bc, err := repo.BlobCreate(BlobWithDigest(newBlobDigest))
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
				bc, err = repo.BlobCreate()
				if err != nil {
					t.Errorf("failed to create new manifest: %v", err)
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

}
