package types

import (
	"errors"
	"testing"

	digest "github.com/sudo-bmitch/oci-digest"
)

func TestIndex(t *testing.T) {
	t.Parallel()
	// setup some sample index structs, empty, one entry, three entries, tags, annotations, children
	tagA, tagB, tagC, tagD, tagIndex := "A", "B", "C", "D", "index"
	digA, err := digest.FromString("A")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descA := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digA,
		Size:      1,
	}
	descATag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digA,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagA},
	}
	digB, err := digest.FromString("B")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descB := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digB,
		Size:      1,
	}
	descBTag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digB,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagB},
	}
	digC, err := digest.FromString("C")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descC := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digC,
		Size:      1,
	}
	descCTag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digC,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagC},
	}
	descCSubj := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digC,
		Size:        1,
		Annotations: map[string]string{AnnotReferrerSubject: digA.String()},
	}
	digD, err := digest.FromString("D")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descD := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digD,
		Size:      1,
	}
	descDTag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digD,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagD},
	}
	descDSubj := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digD,
		Size:        1,
		Annotations: map[string]string{AnnotReferrerSubject: digB.String()},
	}
	digIndex, err := digest.FromString("index")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descIndex := Descriptor{
		MediaType: MediaTypeOCI1ManifestList,
		Digest:    digIndex,
		Size:      5,
	}
	descIndexTag := Descriptor{
		MediaType:   MediaTypeOCI1ManifestList,
		Digest:      digIndex,
		Size:        5,
		Annotations: map[string]string{AnnotRefName: tagIndex},
	}
	iZero := Index{}
	iEmpty := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		Manifests:     []Descriptor{},
	}
	iOne := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		Manifests:     []Descriptor{descA},
	}
	iOneTag := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		Manifests:     []Descriptor{descATag},
	}
	iThree := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		Manifests:     []Descriptor{descB, descC, descD},
	}
	iThreeTag := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		Manifests:     []Descriptor{descBTag, descCTag, descDTag},
	}
	iIndex := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		Manifests:     []Descriptor{descIndex},
	}
	iChildren := Index{
		SchemaVersion:  2,
		MediaType:      MediaTypeOCI1ManifestList,
		Manifests:      []Descriptor{descIndexTag},
		childManifests: []Descriptor{descB, descC, descD},
	}
	iChildrenAdd := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		Manifests:     []Descriptor{descIndexTag},
	}
	iChildrenAdd.AddChildren([]Descriptor{descB, descC, descD})
	iSubject := Index{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCI1ManifestList,
		Manifests:     []Descriptor{descATag, descBTag, descCSubj, descDSubj},
	}

	t.Run("GetDesc", func(t *testing.T) {
		t.Parallel()
		tt := []struct {
			name       string
			i          Index
			arg        string
			expectDesc Descriptor
			expectErr  error
		}{
			{
				name:       "zero",
				i:          iZero,
				arg:        "",
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "zero A tag not found",
				i:          iZero,
				arg:        tagA,
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "empty A digest not found",
				i:          iEmpty,
				arg:        digA.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "empty A tag not found",
				i:          iEmpty,
				arg:        tagA,
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "One A digest parse failure",
				i:          iOne,
				arg:        "sha256:asdf",
				expectDesc: Descriptor{},
				expectErr:  digest.ErrEncodingInvalid,
			},
			{
				name:       "One A digest found",
				i:          iOne,
				arg:        digA.String(),
				expectDesc: descA,
				expectErr:  nil,
			},
			{
				name:       "One A tag not found",
				i:          iOne,
				arg:        tagA,
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "OneTag A tag found",
				i:          iOneTag,
				arg:        tagA,
				expectDesc: descATag,
				expectErr:  nil,
			},
			{
				name:       "OneTag A digest found",
				i:          iOneTag,
				arg:        digA.String(),
				expectDesc: descA,
				expectErr:  nil,
			},
			{
				name:       "OneTag B digest not found",
				i:          iOneTag,
				arg:        digB.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "Three A digest not found",
				i:          iThree,
				arg:        digA.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "Three B digest found",
				i:          iThree,
				arg:        digB.String(),
				expectDesc: descB,
				expectErr:  nil,
			},
			{
				name:       "Three C digest found",
				i:          iThree,
				arg:        digC.String(),
				expectDesc: descC,
				expectErr:  nil,
			},
			{
				name:       "Three D digest found",
				i:          iThree,
				arg:        digD.String(),
				expectDesc: descD,
				expectErr:  nil,
			},
			{
				name:       "Three B tag not found",
				i:          iThree,
				arg:        tagB,
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "ThreeTag A digest not found",
				i:          iThreeTag,
				arg:        digA.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "ThreeTag B digest found",
				i:          iThreeTag,
				arg:        digB.String(),
				expectDesc: descB,
				expectErr:  nil,
			},
			{
				name:       "ThreeTag B tag not found",
				i:          iThreeTag,
				arg:        tagB,
				expectDesc: descBTag,
				expectErr:  nil,
			},
			{
				name:       "Index A digest not found",
				i:          iIndex,
				arg:        digA.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "Index index digest found",
				i:          iIndex,
				arg:        digIndex.String(),
				expectDesc: descIndex,
				expectErr:  nil,
			},
			{
				name:       "Index index tag not found",
				i:          iIndex,
				arg:        tagIndex,
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "Children A digest not found",
				i:          iChildren,
				arg:        digA.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "Children B digest found",
				i:          iChildren,
				arg:        digB.String(),
				expectDesc: descB,
				expectErr:  nil,
			},
			{
				name:       "Children A tag not found",
				i:          iChildren,
				arg:        tagA,
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "ChildrenAdd A digest not found",
				i:          iChildrenAdd,
				arg:        digA.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "ChildrenAdd B tag not found",
				i:          iChildrenAdd,
				arg:        tagB,
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "ChildrenAdd B digest found",
				i:          iChildrenAdd,
				arg:        digB.String(),
				expectDesc: descB,
				expectErr:  nil,
			},
			{
				name:       "ChildrenAdd D digest found",
				i:          iChildrenAdd,
				arg:        digD.String(),
				expectDesc: descD,
				expectErr:  nil,
			},
			{
				name:       "Index index digest found",
				i:          iChildren,
				arg:        digIndex.String(),
				expectDesc: descIndex,
				expectErr:  nil,
			},
			{
				name:       "Index index tag found",
				i:          iChildren,
				arg:        tagIndex,
				expectDesc: descIndexTag,
				expectErr:  nil,
			},
		}
		for _, tc := range tt {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				resultDesc, resultErr := tc.i.GetDesc(tc.arg)
				if tc.expectErr != nil {
					if resultErr == nil {
						t.Errorf("did not fail, expected %v", tc.expectErr)
					} else if !errors.Is(resultErr, tc.expectErr) && resultErr.Error() != tc.expectErr.Error() {
						t.Errorf("unexpected error, expected %v, received %v", tc.expectErr, resultErr)
					}
					return
				}
				if resultErr != nil {
					t.Errorf("unexpected error, received %v", resultErr)
					return
				}
				if !testDescEqual(resultDesc, tc.expectDesc) {
					t.Errorf("descriptor mismatch, expected %v, received %v", tc.expectDesc, resultDesc)
				}
			})
		}
	})

	t.Run("GetByAnnotation", func(t *testing.T) {
		t.Parallel()
		tt := []struct {
			name       string
			i          Index
			key, val   string
			expectDesc Descriptor
			expectErr  error
		}{
			{
				name:       "zero",
				i:          iZero,
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "Three not found",
				i:          iThree,
				key:        AnnotReferrerSubject,
				val:        digB.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "ThreeTag B tag found",
				i:          iThreeTag,
				key:        AnnotRefName,
				val:        tagB,
				expectDesc: descBTag,
				expectErr:  nil,
			},
			{
				name:       "ThreeTag not found",
				i:          iThreeTag,
				key:        AnnotReferrerSubject,
				val:        tagB,
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "Children not found",
				i:          iChildren,
				key:        AnnotReferrerSubject,
				val:        digA.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "Subject A found",
				i:          iSubject,
				key:        AnnotReferrerSubject,
				val:        digA.String(),
				expectDesc: descCSubj,
			},
			{
				name:       "Subject B found",
				i:          iSubject,
				key:        AnnotReferrerSubject,
				val:        digB.String(),
				expectDesc: descDSubj,
			},
			{
				name:       "Subject C not found",
				i:          iSubject,
				key:        AnnotReferrerSubject,
				val:        digC.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
			{
				name:       "Subject D found",
				i:          iSubject,
				key:        AnnotReferrerSubject,
				val:        digD.String(),
				expectDesc: Descriptor{},
				expectErr:  ErrNotFound,
			},
		}
		for _, tc := range tt {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				resultDesc, resultErr := tc.i.GetByAnnotation(tc.key, tc.val)
				if tc.expectErr != nil {
					if resultErr == nil {
						t.Errorf("did not fail, expected %v", tc.expectErr)
					} else if !errors.Is(resultErr, tc.expectErr) && resultErr.Error() != tc.expectErr.Error() {
						t.Errorf("unexpected error, expected %v, received %v", tc.expectErr, resultErr)
					}
					return
				}
				if resultErr != nil {
					t.Errorf("unexpected error, received %v", resultErr)
					return
				}
				if !testDescEqual(resultDesc, tc.expectDesc) {
					t.Errorf("descriptor mismatch, expected %v, received %v", tc.expectDesc, resultDesc)
				}
			})
		}
	})
}

func TestAddDesc(t *testing.T) {
	t.Parallel()
	// setup some sample index structs, empty, one entry, three entries, tags, annotations, children
	tagA, tagB, tagC, tagD, tagIndex := "A", "B", "C", "D", "index"
	digA, err := digest.FromString("A")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	digA2, err := digest.FromString("A2")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descA := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digA,
		Size:      1,
	}
	descATag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digA,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagA},
	}
	digB, err := digest.FromString("B")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descB := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digB,
		Size:      1,
	}
	descBTag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digB,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagB},
	}
	digC, err := digest.FromString("C")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descC := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digC,
		Size:      1,
	}
	descCTag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digC,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagC},
	}
	descCSubj := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digC,
		Size:        1,
		Annotations: map[string]string{AnnotReferrerSubject: digA.String()},
	}
	digD, err := digest.FromString("D")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descD := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digD,
		Size:      1,
	}
	// descDTag := Descriptor{
	// 	MediaType:   MediaTypeOCI1Manifest,
	// 	Digest:      digD,
	// 	Size:        1,
	// 	Annotations: map[string]string{AnnotRefName: tagD},
	// }
	// descDSubj := Descriptor{
	// 	MediaType:   MediaTypeOCI1Manifest,
	// 	Digest:      digD,
	// 	Size:        1,
	// 	Annotations: map[string]string{AnnotReferrerSubject: digB.String()},
	// }
	digIndex, err := digest.FromString("index")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	// descIndex := Descriptor{
	// 	MediaType: MediaTypeOCI1ManifestList,
	// 	Digest:    digIndex,
	// 	Size:      5,
	// }
	descIndexTag := Descriptor{
		MediaType:   MediaTypeOCI1ManifestList,
		Digest:      digIndex,
		Size:        5,
		Annotations: map[string]string{AnnotRefName: tagIndex},
	}
	_ = tagD
	tt := []struct {
		name string
		iIn  Index
		dAdd Descriptor
		opts []IndexOpt
		iOut Index
	}{
		{
			name: "Zero Add A",
			iIn:  Index{},
			dAdd: descA,
			iOut: Index{
				Manifests: []Descriptor{descA},
			},
		},
		{
			name: "A Add A",
			iIn: Index{
				Manifests: []Descriptor{descA},
			},
			dAdd: descA,
			iOut: Index{
				Manifests: []Descriptor{descA},
			},
		},
		{
			name: "A tag Add A",
			iIn: Index{
				Manifests: []Descriptor{descATag},
			},
			dAdd: descA,
			iOut: Index{
				Manifests: []Descriptor{descATag},
			},
		},
		{
			name: "A Add B",
			iIn: Index{
				Manifests: []Descriptor{descA},
			},
			dAdd: descB,
			iOut: Index{
				Manifests: []Descriptor{descA, descB},
			},
		},
		{
			name: "ABC Add tag to B",
			iIn: Index{
				Manifests: []Descriptor{descA, {
					MediaType: MediaTypeOCI1Manifest,
					Digest:    digB,
					Size:      1,
				}, descC},
			},
			dAdd: descBTag,
			iOut: Index{
				Manifests: []Descriptor{descA, descBTag, descC},
			},
		},
		{
			name: "ABC children Add tag to child B",
			iIn: Index{
				Manifests: []Descriptor{descIndexTag},
				childManifests: []Descriptor{descA, {
					MediaType: MediaTypeOCI1Manifest,
					Digest:    digB,
					Size:      1,
				}, descC},
			},
			dAdd: descBTag,
			iOut: Index{
				Manifests:      []Descriptor{descIndexTag, descBTag},
				childManifests: []Descriptor{descA, descC},
			},
		},
		{
			name: "ABC tag Add replacement A tag",
			iIn: Index{
				Manifests: []Descriptor{{
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digA,
					Size:        1,
					Annotations: map[string]string{AnnotRefName: tagA},
				}, descBTag, descCTag},
			},
			dAdd: Descriptor{
				MediaType:   MediaTypeOCI1Manifest,
				Digest:      digA2,
				Size:        2,
				Annotations: map[string]string{AnnotRefName: tagA},
			},
			iOut: Index{
				Manifests: []Descriptor{descA, descBTag, descCTag, {
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digA2,
					Size:        2,
					Annotations: map[string]string{AnnotRefName: tagA},
				}},
			},
		},
		{
			name: "ABC subject Add replacement subject to A",
			iIn: Index{
				Manifests: []Descriptor{descATag, descBTag, descCSubj},
			},
			dAdd: Descriptor{
				MediaType:   MediaTypeOCI1Manifest,
				Digest:      digD,
				Size:        1,
				Annotations: map[string]string{AnnotReferrerSubject: digA.String()},
			},
			iOut: Index{
				Manifests: []Descriptor{descATag, descBTag, {
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digD,
					Size:        1,
					Annotations: map[string]string{AnnotReferrerSubject: digA.String()},
				}},
			},
		},
		{
			name: "ABCD Add IndexTag with Children",
			iIn: Index{
				Manifests: []Descriptor{descA, descB, descC, descD},
			},
			dAdd: descIndexTag,
			opts: []IndexOpt{IndexWithChildren([]Descriptor{descB, descC, descD})},
			iOut: Index{
				Manifests:      []Descriptor{descA, descIndexTag},
				childManifests: []Descriptor{descB, descC, descD},
			},
		},
	}
	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.iIn.AddDesc(tc.dAdd, tc.opts...)
			if !testIndexEqual(tc.iIn, tc.iOut) {
				t.Errorf("index mismatch, expected %v, received %v", tc.iOut, tc.iIn)
			}
		})
	}
}

func TestRmDesc(t *testing.T) {
	t.Parallel()
	// setup some sample index structs, empty, one entry, three entries, tags, annotations, children
	tagA, tagB, tagC, tagD, tagIndex := "A", "B", "C", "D", "index"
	_, _ = tagD, tagIndex
	digA, err := digest.FromString("A")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	// digA2, err := digest.FromString("A2")
	// if err != nil {
	// 	t.Fatalf("failed to generate digest: %v", err)
	// }
	descA := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digA,
		Size:      1,
	}
	descATag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digA,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagA},
	}
	// descA2Tag := Descriptor{
	// 	MediaType:   MediaTypeOCI1Manifest,
	// 	Digest:      digA2,
	// 	Size:        2,
	// 	Annotations: map[string]string{AnnotRefName: tagA},
	// }
	digB, err := digest.FromString("B")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descB := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digB,
		Size:      1,
	}
	descBTag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digB,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagB},
	}
	digC, err := digest.FromString("C")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	descC := Descriptor{
		MediaType: MediaTypeOCI1Manifest,
		Digest:    digC,
		Size:      1,
	}
	descCTag := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digC,
		Size:        1,
		Annotations: map[string]string{AnnotRefName: tagC},
	}
	descCSubj := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digC,
		Size:        1,
		Annotations: map[string]string{AnnotReferrerSubject: digA.String()},
	}
	digD, err := digest.FromString("D")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	// descD := Descriptor{
	// 	MediaType: MediaTypeOCI1Manifest,
	// 	Digest:    digD,
	// 	Size:      1,
	// }
	// descDTag := Descriptor{
	// 	MediaType:   MediaTypeOCI1Manifest,
	// 	Digest:      digD,
	// 	Size:        1,
	// 	Annotations: map[string]string{AnnotRefName: tagD},
	// }
	descDSubj := Descriptor{
		MediaType:   MediaTypeOCI1Manifest,
		Digest:      digD,
		Size:        1,
		Annotations: map[string]string{AnnotReferrerSubject: digB.String()},
	}
	digIndex, err := digest.FromString("index")
	if err != nil {
		t.Fatalf("failed to generate digest: %v", err)
	}
	// descIndex := Descriptor{
	// 	MediaType: MediaTypeOCI1ManifestList,
	// 	Digest:    digIndex,
	// 	Size:      5,
	// }
	descIndexTag := Descriptor{
		MediaType:   MediaTypeOCI1ManifestList,
		Digest:      digIndex,
		Size:        5,
		Annotations: map[string]string{AnnotRefName: tagIndex},
	}
	tt := []struct {
		name string
		iIn  Index
		dRm  Descriptor
		iOut Index
	}{
		{
			name: "Zero Rm A",
			iIn:  Index{},
			dRm:  descA,
			iOut: Index{},
		},
		{
			name: "A Rm B",
			iIn: Index{
				Manifests: []Descriptor{descA},
			},
			dRm: descB,
			iOut: Index{
				Manifests: []Descriptor{descA},
			},
		},
		{
			name: "A Rm A",
			iIn: Index{
				Manifests: []Descriptor{descA},
			},
			dRm: descA,
			iOut: Index{
				Manifests: []Descriptor{},
			},
		},
		{
			// this test is descriptor order dependent
			name: "A,ATag,A2Tag Rm ATag",
			iIn: Index{
				Manifests: []Descriptor{descA, {
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digA,
					Size:        1,
					Annotations: map[string]string{AnnotRefName: tagA},
				}, {
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digA,
					Size:        1,
					Annotations: map[string]string{AnnotRefName: tagA + "2"},
				}},
			},
			dRm: descATag,
			iOut: Index{
				Manifests: []Descriptor{{
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digA,
					Size:        1,
					Annotations: map[string]string{AnnotRefName: tagA + "2"},
				}},
			},
		},
		{
			// this test is descriptor order dependent
			name: "A2Tag,ATag,A Rm ATag",
			iIn: Index{
				Manifests: []Descriptor{{
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digA,
					Size:        1,
					Annotations: map[string]string{AnnotRefName: tagA},
				}, {
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digA,
					Size:        1,
					Annotations: map[string]string{AnnotRefName: tagA + "2"},
				}, descA},
			},
			dRm: descATag,
			iOut: Index{
				Manifests: []Descriptor{{
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digA,
					Size:        1,
					Annotations: map[string]string{AnnotRefName: tagA + "2"},
				}, descA},
			},
		},
		{
			name: "ABC Tag Rm B",
			iIn: Index{
				Manifests: []Descriptor{descATag, descBTag, descCTag},
			},
			dRm: descB,
			iOut: Index{
				Manifests: []Descriptor{descATag, descCTag},
			},
		},
		{
			name: "ABC child Rm B",
			iIn: Index{
				Manifests:      []Descriptor{descIndexTag},
				childManifests: []Descriptor{descA, descB, descC},
			},
			dRm: descB,
			iOut: Index{
				Manifests:      []Descriptor{descIndexTag},
				childManifests: []Descriptor{descA, descC},
			},
		},
		{
			name: "ABC Tag Rm B Tag",
			iIn: Index{
				Manifests: []Descriptor{descATag, {
					MediaType:   MediaTypeOCI1Manifest,
					Digest:      digB,
					Size:        1,
					Annotations: map[string]string{AnnotRefName: tagB},
				}, descCTag},
			},
			dRm: descBTag,
			iOut: Index{
				Manifests: []Descriptor{descATag, descB, descCTag},
			},
		},
		{
			name: "ABC Tag Rm A by tag",
			iIn: Index{
				Manifests: []Descriptor{descATag, descBTag, descCTag},
			},
			dRm: Descriptor{
				Annotations: map[string]string{AnnotRefName: tagA},
			},
			iOut: Index{
				Manifests: []Descriptor{descBTag, descCTag},
			},
		},
		{
			name: "ABCD Subject Rm C by subject",
			iIn: Index{
				Manifests: []Descriptor{descATag, descBTag, descCSubj, descDSubj},
			},
			dRm: Descriptor{
				Annotations: map[string]string{AnnotReferrerSubject: digA.String()},
			},
			iOut: Index{
				Manifests: []Descriptor{descATag, descBTag, descDSubj},
			},
		},
	}
	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.iIn.RmDesc(tc.dRm)
			if !testIndexEqual(tc.iIn, tc.iOut) {
				t.Errorf("index mismatch, expected %v, received %v", tc.iOut, tc.iIn)
			}
		})
	}
}

// TODO: test AddDesc, tt with index, desc to add, result. test digests, tags, referrers, children

// TODO: test RmDesc, tt with index, desc to remove, result. test digests, tags, referrers, children

func testDescEqual(a, b Descriptor) bool {
	if a.MediaType != b.MediaType ||
		a.Size != b.Size ||
		a.Digest != b.Digest ||
		a.ArtifactType != b.ArtifactType {
		return false
	}
	if len(a.Annotations) != len(b.Annotations) {
		return false
	}
	if len(a.Annotations) > 0 {
		for k, v := range a.Annotations {
			if b.Annotations[k] != v {
				return false
			}
		}
	}
	return true
}

func testIndexEqual(a, b Index) bool {
	if len(a.Manifests) != len(b.Manifests) ||
		len(a.childManifests) != len(b.childManifests) ||
		len(a.Annotations) != len(b.Annotations) ||
		a.ArtifactType != b.ArtifactType {
		return false
	}
	for i, dA := range a.Manifests {
		if !testDescEqual(dA, b.Manifests[i]) {
			found := false
			for _, dB := range b.Manifests {
				if testDescEqual(dA, dB) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	for i, dA := range a.childManifests {
		if !testDescEqual(dA, b.childManifests[i]) {
			found := false
			for _, dB := range b.childManifests {
				if testDescEqual(dA, dB) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	for k, vA := range a.Annotations {
		vB, ok := b.Annotations[k]
		if !ok || vA != vB {
			return false
		}
	}
	return true
}
