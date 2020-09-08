// Copyright (c) 2017 Tigera, Inc. All rights reserved.
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

package routereflector

import (
	"os"
	"strings"
)

func parseEnv() (string, map[string]*string) {
	zoneLable := os.Getenv("ROUTE_REFLECTOR_ZONE_LABEL")

	incompatibleLabels := map[string]*string{}
	if v, ok := os.LookupEnv("ROUTE_REFLECTOR_INCOMPATIBLE_NODE_LABELS"); ok {
		for _, l := range strings.Split(v, ",") {
			key, value := getKeyValue(strings.Trim(l, " "))
			if strings.Contains(l, "=") {
				incompatibleLabels[key] = &value
			} else {
				incompatibleLabels[key] = nil
			}
		}
	}

	return zoneLable, incompatibleLabels
}

func getKeyValue(label string) (string, string) {
	keyValue := strings.Split(label, "=")
	if len(keyValue) == 1 {
		keyValue[1] = ""
	}

	return keyValue[0], keyValue[1]
}
