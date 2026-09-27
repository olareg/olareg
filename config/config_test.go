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
