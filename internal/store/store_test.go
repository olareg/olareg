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

package store

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"slices"
	"testing"
	"time"

	digest "github.com/sudo-bmitch/oci-digest"

	"github.com/olareg/olareg/config"
	"github.com/olareg/olareg/internal/copy"
	"github.com/olareg/olareg/internal/gendata"
	"github.com/olareg/olareg/types"
)

// verify interface implementation
var (
	_ Store = &dir{}
	_ Repo  = &dirRepo{}
	_ Store = &mem{}
	_ Repo  = &memRepo{}
)

// func init() {
// 	godbg.SignalTrace()
// }

func TestStore(t *testing.T) {
	RegisterDefaults()
	gen1 := gendata.New(t)
	_, err := gen1.GenIndex(gendata.OptsIndex{
		Tags: []string{"tag1"},
	})
	if err != nil {
		t.Fatalf("failed to generate sample data: %v", err)
	}
	gen2 := gendata.New(t)
	_, err = gen2.GenIndex(gendata.OptsIndex{
		Tags: []string{"tag2"},
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
	tempDirExisting := t.TempDir()
	err = copy.Copy(tempDirExisting, "./../../testdata")
	if err != nil {
		t.Errorf("failed to copy testdata to tempDir: %v", err)
		return
	}
	tempDirRO := t.TempDir()
	err = copy.Copy(tempDirRO, "./../../testdata")
	if err != nil {
		t.Errorf("failed to copy testdata to tempDir: %v", err)
		return
	}
	tt := []struct {
		name            string
		conf            config.ConfigStorage
		opts            []Opts
		expectErr       error
		expectEphemeral bool
		existingData    bool
	}{
		{
			name: "unknown",
			conf: config.ConfigStorage{
				StoreType: "unknown",
			},
			expectErr: types.ErrNotFound,
		},
		{
			name: "mem",
			conf: config.ConfigStorage{
				StoreType: config.StoreMem,
			},
			expectEphemeral: true,
		},
		{
			name: "dir-empty",
			conf: config.ConfigStorage{
				StoreType: config.StoreDir,
				RootDir:   t.TempDir(),
			},
		},
		{
			name: "dir-existing",
			conf: config.ConfigStorage{
				StoreType: config.StoreDir,
				RootDir:   tempDirExisting,
			},
			existingData: true,
		},
		{
			name: "mem-dir",
			conf: config.ConfigStorage{
				StoreType: config.StoreMem,
				RootDir:   tempDirRO,
			},
			expectEphemeral: true,
			existingData:    true,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// TODO: test read-only
			// TODO: breakup tests into separate sub-tests (t.Run)
			t.Parallel()
			start := time.Now().UTC()
			c := config.Config{Storage: tc.conf}
			c.SetDefaults()
			s, err := New(c.Storage, tc.opts...)
			if tc.expectErr != nil {
				if err == nil {
					t.Fatalf("store.New did not fail, expected %v", tc.expectErr)
				}
				if !errors.Is(err, tc.expectErr) && err.Error() != tc.expectErr.Error() {
					t.Errorf("store.New expected error %v, received %v", tc.expectErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("store.New failed: %v", err)
			}
			if s.IsEphemeral() != tc.expectEphemeral {
				t.Errorf("store.IsEphemeral expected %t, received %t", tc.expectEphemeral, s.IsEphemeral())
			}
			// create a repo
			r, err := s.RepoGet("test")
			if err != nil {
				t.Fatalf("store.RepoGet failed: %v", err)
			}
			// get index
			li, err := r.IndexGet()
			if err != nil {
				t.Errorf("repo.IndexGet failed: %v", err)
			}
			if len(li.Manifests) > 0 {
				t.Errorf("unexpected content in new repo: %v", li.Manifests)
			}
			// create gen1 blobs with BlobCreator
			countBlobs1, countBlobs2 := 0, 0
			for step := range gen1.WalkBlobs() {
				countBlobs1++
				bc, err := r.BlobCreate()
				if err != nil {
					t.Fatalf("failed to create blob: %v", err)
				}
				l, err := bc.Write(step.Bytes)
				if err != nil {
					t.Errorf("failed to write bytes: %v", err)
				}
				if l != len(step.Bytes) {
					t.Errorf("unexpected length, expected %d, received %d", len(step.Bytes), l)
				}
				err = bc.Save(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to save blob: %v", err)
				}
			}
			// delete a blob that was never pushed
			for step := range r.Walk(types.ParseBlobs, false, types.Descriptor{Digest: blobDig, MediaType: types.MediaTypeOCI1Layer, Size: int64(len(blobBytes))}) {
				t.Errorf("walking a never pushed blob returned %v", step.Desc)
			}
			err = r.BlobDelete(blobDig)
			if err == nil || !errors.Is(err, types.ErrNotFound) {
				t.Errorf("deleting a blob before pushing, expected %v, received %v", types.ErrNotFound, err)
			}
			// test BlobCreate concurrent, read content that was pushed (for handling algo change), and cancel
			bc, err := r.BlobCreate()
			if err != nil {
				t.Errorf("failed to create blob: %v", err)
			}
			l, err := bc.Write(blobBytes)
			if err != nil {
				t.Errorf("failed to write bytes: %v", err)
			}
			if len(blobBytes) != l {
				t.Errorf("unexpected length, expected %d, received %d", len(blobBytes), l)
			}
			rdr := bc.Reader()
			readBytes, err := io.ReadAll(rdr)
			if err != nil {
				t.Errorf("failed to read back bytes: %v", err)
			}
			if !bytes.Equal(readBytes, blobBytes) {
				t.Errorf("read did not return written bytes")
			}
			err = bc.Cancel()
			if err != nil {
				t.Errorf("failed to cancel blob create: %v", err)
			}
			// push identical blob multiple times, with BlobPut, BlobCreate
			err = r.BlobPut(blobDig, blobBytes)
			if err != nil {
				t.Errorf("failed to put blob: %v", err)
			}
			bc, err = r.BlobCreate()
			if err != nil {
				t.Errorf("failed to create blob: %v", err)
			}
			l, err = bc.Write(blobBytes)
			if err != nil {
				t.Errorf("failed to write bytes: %v", err)
			}
			if len(blobBytes) != l {
				t.Errorf("unexpected length, expected %d, received %d", len(blobBytes), l)
			}
			err = bc.Save(blobDig)
			if err != nil {
				t.Errorf("failed to save blob: %v", err)
			}
			err = r.BlobPut(blobDig, blobBytes)
			if err != nil {
				t.Errorf("failed to put blob: %v", err)
			}
			rsc, err := r.BlobGet(blobDig)
			if err != nil {
				t.Errorf("failed to get blob: %v", err)
			}
			blobRead, err := io.ReadAll(rsc)
			if err != nil {
				t.Errorf("failed to read blob: %v", err)
			}
			if !bytes.Equal(blobBytes, blobRead) {
				t.Errorf("read did not return written bytes")
			}
			err = rsc.Close()
			if err != nil {
				t.Errorf("failed to close blob reader: %v", err)
			}
			// delete blob and verify it cannot be read again
			err = r.BlobDelete(blobDig)
			if err != nil {
				t.Errorf("failed to delete blob: %v", err)
			}
			rsc, err = r.BlobGet(blobDig)
			if err == nil || !errors.Is(err, types.ErrNotFound) {
				t.Errorf("blob get to a deleted blob, expected %v, received %v", types.ErrNotFound, err)
			}
			if err == nil {
				_ = rsc.Close()
			}
			err = r.BlobDelete(blobDig)
			if err == nil || !errors.Is(err, types.ErrNotFound) {
				t.Errorf("deleting a blob multiple times, expected %v, received %v", types.ErrNotFound, err)
			}
			for step := range r.Walk(types.ParseBlobs, false, types.Descriptor{Digest: blobDig, MediaType: types.MediaTypeOCI1Layer, Size: int64(len(blobBytes))}) {
				t.Errorf("walking a deleted blob returned %v", step.Desc)
			}
			// create gen2 blobs with BlobPut
			for step := range gen2.WalkBlobs() {
				countBlobs2++
				err := r.BlobPut(step.Desc.Digest, step.Bytes)
				if err != nil {
					t.Errorf("failed to put blob: %v", err)
				}
			}
			// push manifests and index, but only include gen1 in the new Index
			countManifests1, countManifests2 := 0, 0
			for step := range gen1.WalkManifests() {
				countManifests1++
				err := r.BlobPut(step.Desc.Digest, step.Bytes)
				if err != nil {
					t.Errorf("failed to put manifest blob: %v", err)
				}
				li.Manifests = append(li.Manifests, step.Desc)
			}
			for step := range gen2.WalkManifests() {
				countManifests2++
				err := r.BlobPut(step.Desc.Digest, step.Bytes)
				if err != nil {
					t.Errorf("failed to put manifest blob: %v", err)
				}
			}
			// set index
			err = r.IndexSet(li)
			if err != nil {
				t.Errorf("failed to set index: %v", err)
			}
			// get index
			li2, err := r.IndexGet()
			if err != nil {
				t.Errorf("failed to get index: %v", err)
			}
			if !slices.EqualFunc(li.Manifests, li2.Manifests, func(a, b types.Descriptor) bool { return a.Equal(b) }) {
				t.Errorf("index from get does not match set")
			}
			// walk repo
			walkBlobs, walkManifests := 0, 0
			keepBlobs := map[digest.Digest]bool{}
			for step := range r.Walk(types.ParseBlobs, true, li.Manifests[len(li.Manifests)-1]) {
				keepBlobs[step.Desc.Digest] = true
				b, err := io.ReadAll(step.Rdr)
				if err != nil {
					t.Errorf("failed to read content: %v", err)
				}
				err = step.Rdr.Close()
				if err != nil {
					t.Errorf("step reader close failed: %v", err)
				}
				if types.MediaTypeManifest(step.Desc.MediaType) {
					walkManifests++
					desc, expB, ok := gen1.Manifest(step.Desc.Digest)
					if !ok {
						t.Errorf("walk returned a manifest not in gen1: %s", step.Desc.Digest.String())
					}
					if !step.Desc.Digest.Equal(desc.Digest) || step.Desc.Size != desc.Size || step.Desc.MediaType != desc.MediaType || step.Desc.ArtifactType != desc.ArtifactType {
						t.Errorf("walked descriptor mismatch, expected %v, received %v", desc, step.Desc)
					}
					if !bytes.Equal(expB, b) {
						t.Errorf("walked bytes mismatch for digest %v", step.Desc.Digest.String())
					}
				} else {
					walkBlobs++
					desc, expB, ok := gen1.Blob(step.Desc.Digest)
					if !ok {
						t.Errorf("walk returned a blob not in gen1: %s", step.Desc.Digest.String())
					}
					if !step.Desc.Digest.Equal(desc.Digest) || step.Desc.Size != desc.Size || step.Desc.MediaType != desc.MediaType || step.Desc.ArtifactType != desc.ArtifactType {
						t.Errorf("walked descriptor mismatch, expected %v, received %v", desc, step.Desc)
					}
					if !bytes.Equal(expB, b) {
						t.Errorf("walked bytes mismatch for digest %v", step.Desc.Digest.String())
					}
				}
			}
			// verify the count
			if walkBlobs != countBlobs1 {
				t.Errorf("walk blob count expected %d, received %d", countBlobs1, walkBlobs)
			}
			if walkManifests != countManifests1 {
				t.Errorf("walk manifest count expected %d, received %d", countManifests1, walkManifests)
			}
			// run a GC with cutoff set to start (minus 5 sec to deal with filesystem timestamp differences)
			err = r.GC(start.Add(time.Second*-5), keepBlobs)
			if err != nil {
				t.Errorf("failed to GC: %v", err)
			}
			// get blobs and blob meta from gen1 and gen2, verify no blobs are deleted, but gen2 manifests
			for step := range gen1.WalkBlobs() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen1 blob metadata %s: %v", step.Desc.Digest.String(), err)
				}
				rdr, err := r.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen1 blob %s: %v", step.Desc.Digest.String(), err)
				}
				b, err := io.ReadAll(rdr)
				if err != nil {
					t.Errorf("failed to read gen1 blob %s: %v", step.Desc.Digest.String(), err)
				}
				if !bytes.Equal(b, step.Bytes) {
					t.Errorf("gen1 blob bytes mismatch for %s", step.Desc.Digest.String())
				}
				err = rdr.Close()
				if err != nil {
					t.Errorf("failed to close gen1 blob %s: %v", step.Desc.Digest.String(), err)
				}
			}
			for step := range gen1.WalkManifests() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen1 manifest %s: %v", step.Desc.Digest.String(), err)
				}
				rdr, err := r.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen1 manifest %s: %v", step.Desc.Digest.String(), err)
				}
				b, err := io.ReadAll(rdr)
				if err != nil {
					t.Errorf("failed to read gen1 manifest %s: %v", step.Desc.Digest.String(), err)
				}
				if !bytes.Equal(b, step.Bytes) {
					t.Errorf("gen1 manifest bytes mismatch for %s", step.Desc.Digest.String())
				}
				err = rdr.Close()
				if err != nil {
					t.Errorf("failed to close gen1 manifest %s: %v", step.Desc.Digest.String(), err)
				}
			}
			for step := range gen2.WalkBlobs() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen2 blob %s: %v", step.Desc.Digest.String(), err)
				}
				rdr, err := r.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen2 blob %s: %v", step.Desc.Digest.String(), err)
				}
				b, err := io.ReadAll(rdr)
				if err != nil {
					t.Errorf("failed to read gen2 blob %s: %v", step.Desc.Digest.String(), err)
				}
				if !bytes.Equal(b, step.Bytes) {
					t.Errorf("gen2 blob bytes mismatch for %s", step.Desc.Digest.String())
				}
				err = rdr.Close()
				if err != nil {
					t.Errorf("failed to close gen2 blob %s: %v", step.Desc.Digest.String(), err)
				}
			}
			for step := range gen2.WalkManifests() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen2 manifest %s: %v", step.Desc.Digest.String(), err)
				}
				rdr, err := r.BlobGet(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen2 manifest %s: %v", step.Desc.Digest.String(), err)
				}
				b, err := io.ReadAll(rdr)
				if err != nil {
					t.Errorf("failed to read gen2 manifest %s: %v", step.Desc.Digest.String(), err)
				}
				if !bytes.Equal(b, step.Bytes) {
					t.Errorf("gen2 manifest bytes mismatch for %s", step.Desc.Digest.String())
				}
				err = rdr.Close()
				if err != nil {
					t.Errorf("failed to close gen2 manifest %s: %v", step.Desc.Digest.String(), err)
				}
			}
			// run a GC with cutoff set to now
			err = r.GC(time.Now(), keepBlobs)
			if err != nil {
				t.Errorf("failed to GC: %v", err)
			}
			// get blobs and blob meta, verify gen2 blobs are deleted
			for step := range gen1.WalkBlobs() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen1 blob %s: %v", step.Desc.Digest.String(), err)
				}
			}
			for step := range gen1.WalkManifests() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if err != nil {
					t.Errorf("failed to get gen1 manifest %s: %v", step.Desc.Digest.String(), err)
				}
			}
			for step := range gen2.WalkBlobs() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if !errors.Is(err, types.ErrNotFound) {
					t.Errorf("expected gen2 blob %s to return not found, received: %v", step.Desc.Digest.String(), err)
				}
			}
			for step := range gen2.WalkManifests() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if !errors.Is(err, types.ErrNotFound) {
					t.Errorf("expected gen2 manifest %s to return not found, received: %v", step.Desc.Digest.String(), err)
				}
			}
			// delete manifest blobs from gen1
			for step := range gen1.WalkManifests() {
				err := r.BlobDelete(step.Desc.Digest)
				if err != nil {
					t.Errorf("blob delete failed: %v", err)
				}
			}
			// run another walk with manifests missing
			keepBlobs = map[digest.Digest]bool{}
			for step := range r.Walk(types.ParseBlobs, false, li.Manifests[len(li.Manifests)-1]) {
				keepBlobs[step.Desc.Digest] = true
			}
			// run another GC
			err = r.GC(time.Now(), keepBlobs)
			if err != nil {
				t.Errorf("failed to GC: %v", err)
			}
			// verify everything from gen1 is gone
			for step := range gen1.WalkBlobs() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if !errors.Is(err, types.ErrNotFound) {
					t.Errorf("expected gen1 blob %s to return not found, received: %v", step.Desc.Digest.String(), err)
				}
			}
			for step := range gen1.WalkManifests() {
				_, err := r.BlobMeta(step.Desc.Digest)
				if !errors.Is(err, types.ErrNotFound) {
					t.Errorf("expected gen1 manifest %s to return not found, received: %v", step.Desc.Digest.String(), err)
				}
			}
			// pull existing data from underlying directory
			if tc.existingData {
				rExist, err := s.RepoGet("testrepo")
				if err != nil {
					t.Errorf("failed to get existing repo: %v", err)
				}
				li, err := rExist.IndexGet()
				if err != nil {
					t.Errorf("failed to get index: %v", err)
				}
				for _, desc := range li.Manifests {
					rsc, err = rExist.BlobGet(desc.Digest)
					if err != nil {
						t.Errorf("failed to get blob: %v", err)
					}
					if rsc != nil {
						_ = rsc.Close()
					}
				}
				err = rExist.Close()
				if err != nil {
					t.Errorf("failed to close existing repo: %v", err)
				}
			}
			// close repo
			err = r.Close()
			if err != nil {
				t.Errorf("repo.Close filed: %v", err)
			}
			// close store
			err = s.Close()
			if err != nil {
				t.Errorf("store.Close failed: %v", err)
			}
			// TODO: for persistent repo, reopen, verify content remains, and close again
			// TODO: for persistent directory, verify empty repo directory was deleted after GC
		})
	}
}
