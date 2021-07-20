// Copyright (c) 2020 IBM Corporation All rights reserved.
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
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// isNodeReady looks for node ready status condition
func isNodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == "True"
		}
	}

	return false
}

// isNodeSchedulable looks for node unschedulable spec and taints
func isNodeSchedulable(node *corev1.Node) bool {
	if node.Spec.Unschedulable == true {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if _, ok := notReadyTaints[taint.Key]; ok {
			return false
		}
	}

	return true
}

// isNodeCompatible checks compatibility by labels
func isNodeCompatible(node *corev1.Node, antyAfiinity map[string]*string) bool {
	for k, v := range node.GetLabels() {
		if iv, ok := antyAfiinity[k]; ok && (iv == nil || *iv == v) {
			return false
		}
	}

	return true
}

// getKeyValue splits string by '='
func getKeyValue(label string) (string, string) {
	keyValue := strings.Split(label, "=")
	if len(keyValue) == 1 {
		keyValue[1] = ""
	}

	return keyValue[0], keyValue[1]
}

// orDefaultString returns default string value if original is nil
func orDefaultString(value *string, defaultValue string) string {
	if value == nil {
		return defaultValue
	}
	return *value
}

// orDefaultString returns default int value if original is nil
func orDefaultInt(value *int, defaultValue int) int {
	if value == nil {
		return defaultValue
	}
	return *value
}

// orDefaultString returns default float value if original is nil
func orDefaultFloat(value *float32, defaultValue float32) float32 {
	if value == nil {
		return defaultValue
	}
	return *value
}
