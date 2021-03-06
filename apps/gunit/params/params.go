// Copyright 2016 Palantir Technologies, Inc.
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

package params

import (
	"regexp"
	"sort"

	"github.com/palantir/pkg/matcher"
	"github.com/pkg/errors"
)

type GUnit struct {
	// Tags group tests into different sets. The key is the name of the tag and the value is a matcher.NamesPathsCfg
	// that specifies the rules for matching the tests that are part of the tag. Any test that matches the provided
	// matcher is considered part of the tag.
	Tags map[string]matcher.Matcher

	// Exclude specifies the files that should be excluded from tests.
	Exclude matcher.Matcher
}

func (p *GUnit) Validate() error {
	var invalidTagNames []string
	if len(p.Tags) > 0 {
		for k := range p.Tags {
			if !validTagName(k) {
				invalidTagNames = append(invalidTagNames, k)
			}
		}
	}
	if len(invalidTagNames) > 0 {
		sort.Strings(invalidTagNames)
		return errors.Errorf("invalid tag names: %v", invalidTagNames)
	}
	return nil
}

var tagRegExp = regexp.MustCompile(`[A-Za-z0-9_-]+`)

func validTagName(tag string) bool {
	return len(tagRegExp.ReplaceAllString(tag, "")) == 0
}
