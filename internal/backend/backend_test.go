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

package backend

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"slices"
	"testing"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/gendata"
	"github.com/olareg/olareg/internal/store"
	"github.com/olareg/olareg/types"
)

func TestBackend(t *testing.T) {
	store.RegisterDefaults()
	tag1a, tag1b, tag1c := "tag1a", "tag1b", "tag1c"
	tag2a, tag2b, tag2c := "tag2a", "tag2b", "tag2c"
	gen1 := gendata.New(t)
	dig1Ind, err := gen1.GenIndex(gendata.OptsIndex{
		Tags: []string{tag1a, tag1b, tag1c},
	})
	if err != nil {
		t.Fatalf("failed to generate sample data: %v", err)
	}
	desc1Ind, ok := gen1.Desc(dig1Ind)
	if !ok {
		t.Fatalf("failed to get descriptor for index")
	}
	_, err = gen1.GenImage(gendata.OptsImage{
		ArtifactType: "application/example.artifact",
		LayerOpts: []gendata.OptsLayer{
			{
				MediaType: "application/example.artifact.layer",
				Size:      1024,
			},
		},
		Subject: desc1Ind,
	})
	if err != nil {
		t.Fatalf("failed to generate sample data: %v", err)
	}
	gen2 := gendata.New(t)
	dig2Ind, err := gen2.GenIndex(gendata.OptsIndex{
		Tags: []string{tag2a, tag2b},
	})
	if err != nil {
		t.Fatalf("failed to generate sample data: %v", err)
	}
	desc2Ind, ok := gen2.Desc(dig2Ind)
	if !ok {
		t.Fatalf("failed to get descriptor for index")
	}
	_, err = gen2.GenImage(gendata.OptsImage{
		Tags: []string{tag2c},
	})
	if err != nil {
		t.Fatalf("failed to generate sample data: %v", err)
	}
	gen1a := gendata.New(t)
	_, err = gen1a.GenImage(gendata.OptsImage{
		ArtifactType: "application/example.artifact2",
		LayerOpts: []gendata.OptsLayer{
			{
				MediaType: "application/example.artifact2.layer",
				Size:      1024,
			},
		},
		Subject: desc1Ind,
	})
	if err != nil {
		t.Fatalf("failed to generate sample data: %v", err)
	}
	gen2a := gendata.New(t)
	_, err = gen2a.GenImage(gendata.OptsImage{
		ArtifactType: "application/example.artifact2",
		LayerOpts: []gendata.OptsLayer{
			{
				MediaType: "application/example.artifact2.layer",
				Size:      1024,
			},
		},
		Subject: desc2Ind,
	})
	if err != nil {
		t.Fatalf("failed to generate sample data: %v", err)
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	blobBytes := make([]byte, 4096)
	if _, err := rng.Read(blobBytes); err != nil {
		t.Fatalf("failed to generate random blob: %v", err)
	}
	blobDig, err := digest.FromBytes(blobBytes)
	if err != nil {
		t.Fatalf("failed to compute blob digest: %v", err)
	}
	blobDig512, err := digest.SHA512.FromBytes(blobBytes)
	if err != nil {
		t.Fatalf("failed to compute blob sha512 digest: %v", err)
	}
	wrongDig, err := digest.FromBytes(blobBytes[:2048])
	if err != nil {
		t.Fatalf("failed to compute blob digest: %v", err)
	}
	boolT := true
	boolF := false
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tt := []struct {
		name   string
		conf   config.Config
		errNew error
	}{
		{
			name:   "undefined",
			errNew: types.ErrNotFound,
		},
		{
			name: "mem",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreMem,
					GC: config.ConfigGC{
						Untagged:          &boolF,
						ReferrersDangling: &boolT,
						ReferrersWithSubj: &boolT,
						Frequency:         time.Minute,
						GracePeriod:       0,
					},
				},
				API: config.ConfigAPI{
					Referrer: config.ConfigAPIReferrer{
						Enabled: &boolT,
					},
					TagHistory: config.ConfigAPITagHistory{
						Enabled: &boolT,
					},
				},
				Log: logger,
			},
		},
		{
			name: "dir",
			conf: config.Config{
				Storage: config.ConfigStorage{
					StoreType: config.StoreDir,
					RootDir:   t.TempDir(),
					GC: config.ConfigGC{
						Untagged:          &boolF,
						ReferrersDangling: &boolT,
						ReferrersWithSubj: &boolT,
						Frequency:         time.Minute,
						GracePeriod:       0,
					},
				},
				API: config.ConfigAPI{
					Referrer: config.ConfigAPIReferrer{
						Enabled: &boolT,
					},
					TagHistory: config.ConfigAPITagHistory{
						Enabled: &boolT,
					},
				},
				Log: logger,
			},
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			start := time.Now().UTC()
			tc.conf.SetDefaults()
			b, err := New(tc.conf)
			if tc.errNew != nil {
				if err == nil {
					t.Fatalf("new did not return an error")
				}
				if !errors.Is(err, tc.errNew) || err.Error() != tc.errNew.Error() {
					t.Fatalf("expected %v, received %v", tc.errNew, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("new failed: %v", err)
			}
			// get/create two repos
			r1, err := b.RepoGet("repo1")
			if err != nil {
				t.Fatalf("failed to get repo1: %v", err)
			}
			r2, err := b.RepoGet("repo2")
			if err != nil {
				t.Fatalf("failed to get repo2: %v", err)
			}
			// push blobs to repo1
			for step := range gen1.WalkBlobs() {
				session, err := r1.UploadCreate()
				if err != nil {
					t.Errorf("failed to create an upload session: %v", err)
					continue
				}
				err = r1.UploadReadFrom(session, bytes.NewReader(step.Bytes), 0)
				if err != nil {
					t.Errorf("failed to upload to session: %v", err)
					continue
				}
				us, err := r1.UploadStatus(session)
				if err != nil {
					t.Errorf("upload status failed: %v", err)
				}
				if us.Size != int64(len(step.Bytes)) {
					t.Errorf("upload status size expected %d, received %d", len(step.Bytes), us.Size)
				}
				err = r1.UploadSave(session, step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to save session: %v", err)
				}
			}
			// push manifests to repo1
			for step := range gen1.WalkManifests() {
				desc, procTags, subjDig, err := r1.ManifestPut(step.Bytes, step.Desc.MediaType, step.Desc.Digest, step.Tags)
				if err != nil {
					t.Errorf("failed to put manifest: %v", err)
					continue
				}
				if desc.MediaType != step.Desc.MediaType || !desc.Digest.Equal(step.Desc.Digest) || desc.Size != step.Desc.Size {
					t.Errorf("manifest put descriptor expected %v, received %v", step.Desc, desc)
				}
				if !slices.Equal(step.Tags, procTags) {
					t.Errorf("manifest put processed tags expected %v, received %v", step.Tags, procTags)
				}
				expSubj, _, err := types.ManifestReferrerDescriptor(step.Bytes, step.Desc)
				if err == nil {
					if !subjDig.Equal(expSubj.Digest) {
						t.Errorf("expected subject %s, received %s", expSubj.Digest.String(), subjDig.String())
					}
				} else if errors.Is(err, types.ErrNotFound) {
					if !subjDig.IsZero() {
						t.Errorf("did not expect a subject digest, received %s", subjDig.String())
					}
				} else {
					t.Errorf("failed to parse manifest for subject: %v", err)
				}
			}
			// push gen1a artifact to repo1
			for step := range gen1a.WalkBlobs() {
				session, err := r1.UploadCreate(BlobWithReader(bytes.NewReader(step.Bytes)), BlobWithDigest(step.Desc.Digest))
				if err != nil {
					t.Errorf("failed to create an upload session: %v", err)
					continue
				}
				if session != "" {
					t.Errorf("expected empty session when using BlobWithReader")
				}
			}
			for step := range gen1a.WalkManifests() {
				desc, procTags, subjDig, err := r1.ManifestPut(step.Bytes, step.Desc.MediaType, step.Desc.Digest, step.Tags)
				if err != nil {
					t.Errorf("failed to put manifest: %v", err)
					continue
				}
				if desc.MediaType != step.Desc.MediaType || !desc.Digest.Equal(step.Desc.Digest) || desc.Size != step.Desc.Size {
					t.Errorf("manifest put descriptor expected %v, received %v", step.Desc, desc)
				}
				if !slices.Equal(step.Tags, procTags) {
					t.Errorf("manifest put processed tags expected %v, received %v", step.Tags, procTags)
				}
				expSubj, _, err := types.ManifestReferrerDescriptor(step.Bytes, step.Desc)
				if err == nil {
					if !subjDig.Equal(expSubj.Digest) {
						t.Errorf("expected subject %s, received %s", expSubj.Digest.String(), subjDig.String())
					}
				} else if errors.Is(err, types.ErrNotFound) {
					if !subjDig.IsZero() {
						t.Errorf("did not expect a subject digest, received %s", subjDig.String())
					}
				} else {
					t.Errorf("failed to parse manifest for subject: %v", err)
				}
			}
			// push blobs to repo2
			for step := range gen2.WalkBlobs() {
				session, err := r2.UploadCreate(BlobWithReader(bytes.NewReader(step.Bytes)), BlobWithDigest(step.Desc.Digest))
				if err != nil {
					t.Errorf("failed to create an upload session: %v", err)
					continue
				}
				if session != "" {
					t.Errorf("expected empty session when using BlobWithReader")
				}
			}
			// push manifests to repo2
			for step := range gen2.WalkManifests() {
				desc, procTags, subjDig, err := r2.ManifestPut(step.Bytes, step.Desc.MediaType, step.Desc.Digest, step.Tags)
				if err != nil {
					t.Errorf("failed to put manifest: %v", err)
					continue
				}
				if desc.MediaType != step.Desc.MediaType || !desc.Digest.Equal(step.Desc.Digest) || desc.Size != step.Desc.Size {
					t.Errorf("manifest put descriptor expected %v, received %v", step.Desc, desc)
				}
				if !slices.Equal(step.Tags, procTags) {
					t.Errorf("manifest put processed tags expected %v, received %v", step.Tags, procTags)
				}
				expSubj, _, err := types.ManifestReferrerDescriptor(step.Bytes, step.Desc)
				if err == nil {
					if !subjDig.Equal(expSubj.Digest) {
						t.Errorf("expected subject %s, received %s", expSubj.Digest.String(), subjDig.String())
					}
				} else if errors.Is(err, types.ErrNotFound) {
					if !subjDig.IsZero() {
						t.Errorf("did not expect a subject digest, received %s", subjDig.String())
					}
				} else {
					t.Errorf("failed to parse manifest for subject: %v", err)
				}
			}
			// push gen2a artifact to repo2
			for step := range gen2a.WalkBlobs() {
				session, err := r2.UploadCreate(BlobWithReader(bytes.NewReader(step.Bytes)), BlobWithDigest(step.Desc.Digest))
				if err != nil {
					t.Errorf("failed to create an upload session: %v", err)
					continue
				}
				if session != "" {
					t.Errorf("expected empty session when using BlobWithReader")
				}
			}
			for step := range gen2a.WalkManifests() {
				desc, procTags, subjDig, err := r2.ManifestPut(step.Bytes, step.Desc.MediaType, step.Desc.Digest, step.Tags)
				if err != nil {
					t.Errorf("failed to put manifest: %v", err)
					continue
				}
				if desc.MediaType != step.Desc.MediaType || !desc.Digest.Equal(step.Desc.Digest) || desc.Size != step.Desc.Size {
					t.Errorf("manifest put descriptor expected %v, received %v", step.Desc, desc)
				}
				if !slices.Equal(step.Tags, procTags) {
					t.Errorf("manifest put processed tags expected %v, received %v", step.Tags, procTags)
				}
				expSubj, _, err := types.ManifestReferrerDescriptor(step.Bytes, step.Desc)
				if err == nil {
					if !subjDig.Equal(expSubj.Digest) {
						t.Errorf("expected subject %s, received %s", expSubj.Digest.String(), subjDig.String())
					}
				} else if errors.Is(err, types.ErrNotFound) {
					if !subjDig.IsZero() {
						t.Errorf("did not expect a subject digest, received %s", subjDig.String())
					}
				} else {
					t.Errorf("failed to parse manifest for subject: %v", err)
				}
			}
			// blob upload edge cases
			session1, err := r1.UploadCreate(BlobWithDigest(blobDig))
			if err != nil {
				t.Errorf("failed to create blob with digest: %v", err)
			}
			err = r1.UploadReadFrom(session1, bytes.NewReader(blobBytes[:2048]), 0)
			if err != nil {
				t.Errorf("failed to upload start of blob: %v", err)
			}
			err = r1.UploadReadFrom(session1, bytes.NewReader(blobBytes[2048:]), 2048)
			if err != nil {
				t.Errorf("failed to upload end of blob: %v", err)
			}
			session2, err := r1.UploadCreate(BlobWithAlgorithm(digest.SHA512))
			if err != nil {
				t.Errorf("failed to create blob with sha512 algorithm: %v", err)
			}
			err = r1.UploadReadFrom(session2, bytes.NewReader(blobBytes), 0)
			if err != nil {
				t.Errorf("failed to upload blob: %v", err)
			}
			session3, err := r1.UploadCreate(BlobWithBytes(blobBytes), BlobWithDigest(blobDig))
			if err != nil {
				t.Errorf("failed to create blob with bytes: %v", err)
			}
			if session3 != "" {
				t.Errorf("session not empty when using BlobWithBytes")
			}
			session4, err := r1.UploadCreate(BlobWithAlgorithm(digest.SHA512))
			if err != nil {
				t.Errorf("failed to create blob with sha512 algorithm: %v", err)
			}
			err = r1.UploadReadFrom(session4, bytes.NewReader(blobBytes), 0)
			if err != nil {
				t.Errorf("failed to upload blob: %v", err)
			}
			_, err = r1.UploadCreate(BlobWithReader(bytes.NewReader(blobBytes)), BlobWithDigest(wrongDig))
			if !errors.Is(err, types.ErrDigestInvalid) {
				t.Errorf("wrong digest using reader, expected %v, received %v", types.ErrDigestInvalid, err)
			}
			_, err = r1.UploadCreate(BlobWithBytes(blobBytes), BlobWithDigest(wrongDig))
			if !errors.Is(err, types.ErrDigestInvalid) {
				t.Errorf("wrong digest using bytes, expected %v, received %v", types.ErrDigestInvalid, err)
			}
			session5, err := r1.UploadCreate()
			if err != nil {
				t.Errorf("failed to create blob: %v", err)
			}
			err = r1.UploadReadFrom(session5, bytes.NewReader(blobBytes[2048:]), 2048)
			if !errors.Is(err, types.ErrBlobUploadInvalid) {
				t.Errorf("uploading blob with invalid range, expected: %v, received %v", types.ErrBlobUploadInvalid, err)
			}
			err = r1.UploadReadFrom(session5, bytes.NewReader(blobBytes[:2048]), 0)
			if err != nil {
				t.Errorf("failed to upload blob: %v", err)
			}
			err = r1.UploadCancel(session5)
			if err != nil {
				t.Errorf("failed to cancel blob upload: %v", err)
			}
			err = r1.UploadReadFrom(session5, bytes.NewReader(blobBytes[2048:]), 2048)
			if !errors.Is(err, types.ErrNotFound) {
				t.Errorf("uploading blob after cancel, expected: %v, received %v", types.ErrNotFound, err)
			}
			err = r1.UploadSave(session1, blobDig)
			if err != nil {
				t.Errorf("failed saving session1: %v", err)
			}
			err = r1.UploadSave(session2, blobDig512)
			if err != nil {
				t.Errorf("failed saving session2: %v", err)
			}
			err = r1.UploadSave(session4, wrongDig)
			if !errors.Is(err, types.ErrDigestInvalid) {
				t.Errorf("wrong digest using session, expected %v, received %v", types.ErrDigestInvalid, err)
			}
			err = r1.UploadSave(session5, blobDig)
			if !errors.Is(err, types.ErrNotFound) {
				t.Errorf("saving blob after cancel, expected: %v, received %v", types.ErrNotFound, err)
			}
			rsc, err := r1.BlobGet(blobDig)
			if err != nil {
				t.Fatalf("failed to get blob: %v", err)
			}
			readBytes, err := io.ReadAll(rsc)
			if err != nil {
				t.Errorf("failed to read blob: %v", err)
			}
			if !bytes.Equal(readBytes, blobBytes) {
				t.Errorf("blob bytes mismatch")
			}
			err = rsc.Close()
			if err != nil {
				t.Errorf("failed to close reader: %v", err)
			}
			rsc, err = r1.BlobGet(blobDig512)
			if err != nil {
				t.Fatalf("failed to get blob: %v", err)
			}
			readBytes, err = io.ReadAll(rsc)
			if err != nil {
				t.Errorf("failed to read blob: %v", err)
			}
			if !bytes.Equal(readBytes, blobBytes) {
				t.Errorf("blob bytes mismatch")
			}
			err = rsc.Close()
			if err != nil {
				t.Errorf("failed to close reader: %v", err)
			}
			_, err = r1.BlobGet(wrongDig)
			if !errors.Is(err, types.ErrNotFound) {
				t.Errorf("blob get with wrong digest expected %v, received %v", types.ErrNotFound, err)
			}
			// tag listing
			tags := gen1.Tags()
			tl, err := r1.TagList()
			if err != nil {
				t.Errorf("tag listing failed: %v", err)
			}
			for tag, dig := range tags {
				expDesc, ok := gen1.Desc(dig)
				if !ok {
					t.Fatalf("failed to lookup descriptor in generated data: %v", err)
				}
				desc, ok := tl[tag]
				if !ok {
					t.Errorf("tag missing: %s", tag)
					continue
				}
				if desc.MediaType != expDesc.MediaType || !desc.Digest.Equal(expDesc.Digest) || desc.Size != expDesc.Size {
					t.Errorf("tag %s descriptor expected %v, received %v", tag, expDesc, desc)
				}
			}
			// get manifests by digest and tag
			for step := range gen1.WalkManifests() {
				desc, err := r1.ManifestHead(step.Desc.Digest)
				if err != nil {
					t.Errorf("manifest head failed: %v", err)
				}
				if desc.MediaType != step.Desc.MediaType || !desc.Digest.Equal(step.Desc.Digest) || desc.Size != step.Desc.Size {
					t.Errorf("manifest head descriptor expected %v, received %v", step.Desc, desc)
				}
				desc, rsc, err := r1.ManifestGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("manifest get failed: %v", err)
				}
				if desc.MediaType != step.Desc.MediaType || !desc.Digest.Equal(step.Desc.Digest) || desc.Size != step.Desc.Size {
					t.Errorf("manifest get descriptor expected %v, received %v", step.Desc, desc)
				}
				raw, err := io.ReadAll(rsc)
				if err != nil {
					t.Errorf("failed to read manifest: %v", err)
				}
				if !bytes.Equal(raw, step.Bytes) {
					t.Errorf("manifest get did not match expected body")
				}
				err = rsc.Close()
				if err != nil {
					t.Errorf("manifest get reader failed on close: %v", err)
				}
				for _, tag := range step.Tags {
					tagDig, err := r1.TagGet(tag)
					if err != nil {
						t.Errorf("failed to get tag %s: %v", tag, err)
						continue
					}
					if !tagDig.Equal(step.Desc.Digest) {
						t.Errorf("tag digest for %s, expected %v, received %v", tag, step.Desc.Digest.String(), tagDig.String())
					}
				}
				expReferrers := gen1.Referrers(step.Desc.Digest)
				rlDesc, rlInd, err := r1.ReferrerList(step.Desc.Digest)
				if len(expReferrers) == 0 {
					if len(rlInd.Manifests) > 0 {
						t.Errorf("expected empty referrers list, received %v", rlInd)
					}
				} else {
					if err != nil {
						t.Errorf("referrers list failed: %v", err)
					}
					for _, desc := range expReferrers {
						if !slices.ContainsFunc(rlInd.Manifests, func(cur types.Descriptor) bool { return cur.Same(desc) }) {
							t.Errorf("referrers list missing entry: %v", desc)
						}
					}
					if rlDesc.MediaType != types.MediaTypeOCI1ManifestList || rlDesc.Digest.IsZero() || rlDesc.Size == 0 {
						t.Errorf("referrers list descriptor missing expected values: %v", rlDesc)
					}
				}
			}
			// get blobs
			for step := range gen1.WalkBlobs() {
				rsc, err := r1.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get blob %s: %v", step.Desc.Digest.String(), err)
					continue
				}
				raw, err := io.ReadAll(rsc)
				if err != nil {
					t.Errorf("failed to ready blob %s: %v", step.Desc.Digest.String(), err)
				}
				if !bytes.Equal(raw, step.Bytes) {
					t.Errorf("blob bytes mismatch for %s", step.Desc.Digest.String())
				}
				err = rsc.Close()
				if err != nil {
					t.Errorf("failed to close blob reader %s: %v", step.Desc.Digest.String(), err)
				}
			}
			// tag history
			tag1aDig, ok := gen1.Tag(tag1a)
			if !ok {
				t.Errorf("failed to get digest for %s", tag1a)
			}
			tag1aDesc, tag1aRaw, ok := gen1.Manifest(tag1aDig)
			if !ok {
				t.Errorf("failed to get manifest for %s", tag1a)
			}
			loopCount := 3
			for range loopCount {
				err = r1.TagDelete(tag1a)
				if err != nil {
					t.Errorf("failed to delete tag %s: %v", tag1a, err)
				}
				_, _, _, err = r1.ManifestPut(tag1aRaw, tag1aDesc.MediaType, tag1aDig, []string{tag1a})
				if err != nil {
					t.Errorf("failed to put manifest with tag %s: %v", tag1a, err)
				}
			}
			histDesc, histInd, err := r1.TagHistory(tag1a)
			if err != nil {
				t.Errorf("failed to get tag history for %s: %v", tag1a, err)
			}
			if histDesc.MediaType != types.MediaTypeOCI1ManifestList || histDesc.Digest.IsZero() || histDesc.Size <= 0 {
				t.Errorf("history descriptor is not fully defined: %v", histDesc)
			}
			if len(histInd.Manifests) != ((loopCount * 2) + 1) {
				t.Errorf("history for %s expected %d entries, received %d entries: %v", tag1a, ((loopCount * 2) + 1), len(histInd.Manifests), histInd)
			}
			histDesc, histInd, err = r1.TagHistory(tag1b)
			if err != nil {
				t.Errorf("failed to get tag history for %s: %v", tag1b, err)
			}
			if histDesc.MediaType != types.MediaTypeOCI1ManifestList || histDesc.Digest.IsZero() || histDesc.Size <= 0 {
				t.Errorf("history descriptor is not fully defined: %v", histDesc)
			}
			if len(histInd.Manifests) != 1 {
				t.Errorf("history for %s expected %d entries, received %d entries: %v", tag1b, 1, len(histInd.Manifests), histInd)
			}
			_, _, err = r1.TagHistory("missing")
			if !errors.Is(err, types.ErrNotFound) {
				t.Errorf("tag history for missing tag, expected %v, received %v", types.ErrNotFound, err)
			}
			// delete manifest
			err = r2.ManifestDelete(dig2Ind)
			if err != nil {
				t.Errorf("failed to delete %s digest %s: %v", tag2a, dig2Ind.String(), err)
			}
			err = r2.ManifestDelete(dig2Ind)
			if !errors.Is(err, types.ErrNotFound) {
				t.Errorf("repeat deleting manifest, expected %v, received %v", types.ErrNotFound, err)
			}
			err = r2.TagDelete(tag2a)
			if !errors.Is(err, types.ErrNotFound) {
				t.Errorf("deleting tag after manifest removed, expected %v, received %v", types.ErrNotFound, err)
			}
			// delete tag
			err = r2.TagDelete(tag2c)
			if err != nil {
				t.Errorf("failed to delete %s: %v", tag2c, err)
			}
			err = r2.TagDelete(tag2c)
			if !errors.Is(err, types.ErrNotFound) {
				t.Errorf("deleting already deleted tag, expected %v, received %v", types.ErrNotFound, err)
			}
			// blob delete on sample blobs
			err = r1.BlobDelete(blobDig)
			if err != nil {
				t.Errorf("failed to delete blob: %v", err)
			}
			_, err = r1.BlobGet(blobDig)
			if !errors.Is(err, types.ErrNotFound) {
				t.Fatalf("blob get on deleted blob, expected %v, received %v", types.ErrNotFound, err)
			}
			err = r1.BlobDelete(blobDig)
			if !errors.Is(err, types.ErrNotFound) {
				t.Fatalf("blob delete on deleted blob, expected %v, received %v", types.ErrNotFound, err)
			}
			err = r1.BlobDelete(blobDig512)
			if err != nil {
				t.Errorf("failed to delete blob: %v", err)
			}
			_, err = r1.BlobGet(blobDig512)
			if !errors.Is(err, types.ErrNotFound) {
				t.Fatalf("blob get on deleted blob, expected %v, received %v", types.ErrNotFound, err)
			}
			// gc with a cutoff before we pushed anything should not delete anything.
			// Subtract an extra minute to avoid issues with filesystem timestamp granularity.
			r1.gc(start.Add(-1 * time.Minute))
			r2.gc(start.Add(-1 * time.Minute))
			// verify all manifests in repo1 remain, including artifacts, and that blobs in repo2 remain, excluding artifacts if GC removes them with subject
			for step := range gen1.WalkManifests() {
				_, err = r1.ManifestHead(step.Desc.Digest)
				if err != nil {
					t.Errorf("expected manifest to exist, manifest head returned %v", err)
				}
			}
			for step := range gen1.WalkBlobs() {
				rsc, err = r1.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("expected blob to exist, blob get returned %v", err)
				}
				if rsc != nil {
					_ = rsc.Close()
				}
			}
			for step := range gen1a.WalkManifests() {
				_, err = r1.ManifestHead(step.Desc.Digest)
				if err != nil {
					t.Errorf("expected manifest to exist, manifest head returned %v", err)
				}
			}
			for step := range gen1a.WalkBlobs() {
				rsc, err = r1.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("expected blob to exist, blob get returned %v", err)
				}
				if rsc != nil {
					_ = rsc.Close()
				}
			}
			for step := range gen2.WalkBlobs() {
				rsc, err = r2.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("expected blob to exist, blob get returned %v", err)
				}
				if rsc != nil {
					_ = rsc.Close()
				}
			}
			for step := range gen2a.WalkManifests() {
				_, err = r2.ManifestHead(step.Desc.Digest)
				// the artifact should remain until a GC after the grace period
				if !*tc.conf.Storage.GC.ReferrersWithSubj && err != nil {
					t.Errorf("expected manifest to exist, manifest head returned %v", err)
				}
				if *tc.conf.Storage.GC.ReferrersWithSubj && !errors.Is(err, types.ErrNotFound) {
					t.Errorf("expected manifest to be deleted with subject, manifest head returned %v", err)
				}
			}
			// gc with a cutoff in the future should cleanup unreferenced manifests and blobs.
			r1.gc(start.Add(time.Minute * 5))
			r2.gc(start.Add(time.Minute * 5))
			// repo1 should remain (only tag was deleted and recreated)
			for step := range gen1.WalkManifests() {
				_, err = r1.ManifestHead(step.Desc.Digest)
				if err != nil {
					t.Errorf("expected manifest to exist, manifest head returned %v", err)
				}
			}
			for step := range gen1.WalkBlobs() {
				rsc, err = r1.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("expected blob to exist, blob get returned %v", err)
				}
				if rsc != nil {
					_ = rsc.Close()
				}
			}
			for step := range gen1a.WalkManifests() {
				_, err = r1.ManifestHead(step.Desc.Digest)
				if err != nil {
					t.Errorf("expected manifest to exist, manifest head returned %v", err)
				}
			}
			for step := range gen1a.WalkBlobs() {
				rsc, err = r1.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("expected blob to exist, blob get returned %v", err)
				}
				if rsc != nil {
					_ = rsc.Close()
				}
			}
			// repo2 should be cleaned, including referrers and tag history
			for step := range gen2.WalkManifests() {
				_, err = r2.ManifestHead(step.Desc.Digest)
				if !errors.Is(err, types.ErrNotFound) {
					t.Errorf("expected manifest to be gc, expected %v, received %v", types.ErrNotFound, err)
				} else {
					t.Logf("manifest was gc as expected")
				}
			}
			for step := range gen2.WalkBlobs() {
				rsc, err = r2.BlobGet(step.Desc.Digest)
				if !errors.Is(err, types.ErrNotFound) {
					t.Errorf("expected blob to be gc, expected %v, received %v", types.ErrNotFound, err)
				} else {
					t.Logf("blob was gc as expected")
				}
				if rsc != nil {
					_ = rsc.Close()
				}
			}
			for step := range gen2a.WalkManifests() {
				_, err = r2.ManifestHead(step.Desc.Digest)
				if !errors.Is(err, types.ErrNotFound) {
					t.Errorf("expected manifest to be gc, expected %v, received %v", types.ErrNotFound, err)
				}
			}
			for step := range gen2a.WalkBlobs() {
				rsc, err = r2.BlobGet(step.Desc.Digest)
				if !errors.Is(err, types.ErrNotFound) {
					t.Errorf("expected blob to be gc, expected %v, received %v", types.ErrNotFound, err)
				}
				if rsc != nil {
					_ = rsc.Close()
				}
			}
			// TODO: push invalid manifests (missing blobs, invalid url)
			err = b.Close()
			if err != nil {
				t.Errorf("close failed: %v", err)
			}
		})
	}
}
