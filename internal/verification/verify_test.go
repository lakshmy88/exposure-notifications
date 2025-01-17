// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package verification

import (
	"fmt"
	"testing"

	"github.com/googlepartners/exposure-notifications/internal/model"
)

const (
	appPkgName = "com.example.pkg"
)

func TestVerifyRegions(t *testing.T) {
	allRegions := &model.APIConfig{
		AppPackageName:  appPkgName,
		AllowAllRegions: true,
	}
	usCaRegions := &model.APIConfig{
		AppPackageName: appPkgName,
		AllowedRegions: make(map[string]bool),
	}
	usCaRegions.AllowedRegions["US"] = true
	usCaRegions.AllowedRegions["CA"] = true

	cases := []struct {
		Data model.Publish
		Msg  string
		Cfg  *model.APIConfig
	}{
		{
			model.Publish{Regions: []string{"US"}},
			"no allowed regions configured",
			nil,
		},
		{
			model.Publish{Regions: []string{"US"}},
			"",
			allRegions,
		},
		{
			model.Publish{Regions: []string{"US"}},
			"",
			usCaRegions,
		},
		{
			model.Publish{Regions: []string{"US", "CA"}},
			"",
			usCaRegions,
		},
		{
			model.Publish{Regions: []string{"MX"}},
			fmt.Sprintf("application '%v' tried to write unauthorized region: '%v'", appPkgName, "MX"),
			usCaRegions,
		},
	}

	for i, c := range cases {
		err := VerifyRegions(c.Cfg, c.Data)
		if c.Msg == "" && err == nil {
			continue
		}
		if c.Msg == "" && err != nil {
			t.Errorf("%v got %v, wanted no error", i, err)
			continue
		}
		if err.Error() != c.Msg {
			t.Errorf("%v wrong error, got %v, want %v", i, err, c.Msg)
		}
	}
}
