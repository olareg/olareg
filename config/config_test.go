package config

import "testing"

func TestSetDefaults(t *testing.T) {
	c := Config{Storage: ConfigStorage{StoreType: StoreDir}}
	c.SetDefaults()
	if c.API.DeleteEnabled == nil || c.API.PushEnabled == nil || c.API.Blob.DeleteEnabled == nil ||
		c.API.Referrer.Enabled == nil || c.Storage.ReadOnly == nil {
		t.Errorf("bool default values should not be nil")
	}
	if c.API.Manifest.Limit == 0 {
		t.Errorf("manifest limit should not be zero")
	}
	if c.Storage.RootDir == "" {
		t.Errorf("rootDir should not be empty for StoreDir")
	}
}

func TestStoreMarshal(t *testing.T) {
	tt := []struct {
		val    Store
		str    string
		expErr bool
	}{
		{
			val: StoreDir,
			str: "dir",
		},
		{
			val: StoreMem,
			str: "mem",
		},
		{
			val:    StoreUndef,
			str:    "unknown",
			expErr: true,
		},
	}
	for _, tc := range tt {
		t.Run(tc.str, func(t *testing.T) {
			t.Run("marshal", func(t *testing.T) {
				result, err := tc.val.MarshalText()
				if tc.expErr {
					if err == nil {
						t.Errorf("marshal did not fail")
					}
					return
				}
				if err != nil {
					t.Errorf("failed to marshal: %v", err)
					return
				}
				if string(result) != tc.str {
					t.Errorf("expected %s, received %s", tc.str, string(result))
				}
			})
			t.Run("unmarshal", func(t *testing.T) {
				var s Store
				err := s.UnmarshalText([]byte(tc.str))
				if tc.expErr {
					if err == nil {
						t.Errorf("unmarshal did not fail")
					}
					return
				}
				if err != nil {
					t.Errorf("failed to unmarshal: %v", err)
				}
				if tc.val != s {
					t.Errorf("expected %d, received %d", tc.val, s)
				}
			})
		})
	}
}
