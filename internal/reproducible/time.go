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

package reproducible

import (
	"os"
	"strconv"
	"sync"
	"time"
)

const EpocEnv = "SOURCE_DATE_EPOC"

var (
	timeProcOnce sync.Once
	timeProcTime time.Time
)

// TimeNow returns the current time or SOURCE_DATE_EPOC if that is set.
func TimeNow() time.Time {
	timeProcOnce.Do(TimeProcEnv)
	if !timeProcTime.IsZero() {
		return timeProcTime
	}
	return time.Now().UTC()
}

// TimeProcEnv processes the time in the SOURCE_DATE_EPOC environment variable.
// This is typically only run once by [TimeNow] but may be manually run if the environment is modified while the application is running.
func TimeProcEnv() {
	timeProcTime = time.Time{}
	sec := os.Getenv(EpocEnv)
	if sec == "" {
		return
	}
	secI, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return
	}
	timeProcTime = time.Unix(secI, 0).UTC()
}
