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

package status

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	DefaultStatusFile = "status.json"
)

var (
	writeRetryTime      = 4 * time.Second
	writeRetryLogEveryN = 5
)

type ConditionStatus struct {
	Ready  bool
	Reason string
}

type Status struct {
	Readiness  map[string]ConditionStatus
	readyMutex sync.Mutex
	statusFile string
	trigger    chan struct{}
}

func New(file string) *Status {
	st := Status{
		statusFile: file,
		Readiness:  map[string]ConditionStatus{},
	}
	return &st
}

// SetReady sets the status of one ready key and the reason associated with that status.
// Note that the first call to SetReady will start a long-running goroutine to write out the status data to
// file.
func (s *Status) SetReady(key string, ready bool, reason string) {
	s.readyMutex.Lock()
	defer s.readyMutex.Unlock()

	if prev, ok := s.Readiness[key]; !ok || prev.Ready != ready || prev.Reason != reason {
		s.Readiness[key] = ConditionStatus{Ready: ready, Reason: reason}
		s.triggerWriteStatus()
	}
}

// GetReady check the status of the specified ready key, if the key has never
// been set then it is considered not ready (false).
func (s *Status) GetReady(key string) bool {
	s.readyMutex.Lock()
	defer s.readyMutex.Unlock()

	v, ok := s.Readiness[key]
	if !ok {
		return false
	}
	return v.Ready
}

// GetReadiness checks all readiness keys and returns true if all are ready.
// If there are no readiness conditions then it has not been initialized and
// is considered not ready.
func (s *Status) GetReadiness() bool {
	s.readyMutex.Lock()
	defer s.readyMutex.Unlock()

	if len(s.Readiness) == 0 {
		return false
	}
	for _, v := range s.Readiness {
		if !v.Ready {
			return false
		}
	}
	return true
}

// GetNotReadyConditions cycles through all readiness keys and for any that
// are not ready the reasons are combined and returned.
// The output format is '<reason 1>; <reason 2>'.
func (s *Status) GetNotReadyConditions() string {
	s.readyMutex.Lock()
	defer s.readyMutex.Unlock()

	var unready []string
	for _, v := range s.Readiness {
		if !v.Ready {
			unready = append(unready, v.Reason)
		}
	}
	return strings.Join(unready, "; ")

}

// triggerWriteStatus triggers an asynchronous write of this status to file.
// The readyMutex lock should be held by the caller.
func (s *Status) triggerWriteStatus() {
	if s.trigger == nil {
		// We haven't started the writer goroutine yet, create a wakeup channel of length 1.
		s.trigger = make(chan struct{}, 1)
		go func() {
			for _ = range s.trigger {
				if err := s.writeStatus(); err != nil {
					// If we errored writing the status, enter a retry loop.
					for i := 0; err != nil; i++ {
						if i%writeRetryLogEveryN == 0 {
							log.WithError(err).Info("Unable to write status file")
						}
						time.Sleep(writeRetryTime)
						err = s.writeStatus()
					}
					log.Infof("Status file written after previous failure")
				}
			}
		}()
	}

	// Trigger a write by sending a message in the trigger channel. Since we always write out the latest settings, there
	// is no point in having more than 1 pending update - so make this a non-blocking send since the channel has a non-zero
	// capacity.
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// WriteStatus writes out the status in json format.
func (s *Status) writeStatus() error {
	s.readyMutex.Lock()
	b, err := json.Marshal(s)
	filename := s.statusFile
	s.readyMutex.Unlock()

	if err != nil {
		return fmt.Errorf("Failed to marshal readiness: %v", err)
	}

	err = ioutil.WriteFile(filename, b, 0644)
	if err != nil {
		return fmt.Errorf("Failed to write readiness file: %v", err)
	}

	return nil
}

// ReadStatusFile reads in the status file as written by WriteStatus.
func ReadStatusFile(file string) (*Status, error) {
	st := &Status{}
	contents, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(contents, st)
	if err != nil {
		return nil, err
	}

	return st, nil
}
