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
	"io/ioutil"
	"os"
	"strconv"
	"sync"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	eventuallyTimeout = "1s"
	eventuallyPoll    = "100ms"
)

// This section contains unit tests for the Status pkg.
var _ = Describe("Status pkg UTs", func() {
	originalWriteRetryTime := writeRetryTime

	BeforeEach(func() {
		writeRetryTime = 10 * time.Millisecond
	})

	AfterEach(func() {
		writeRetryTime = originalWriteRetryTime
	})

	It("should report failed readiness with no condition statuses", func() {
		st := New("no-needed")
		Expect(st.GetReadiness()).To(BeFalse())
	})

	It("should update the status file when changes happen", func() {
		f, err := ioutil.TempFile("", "test")
		Expect(err).NotTo(HaveOccurred())
		st := New(f.Name())
		defer func() {
			os.Remove(f.Name())
			close(st.trigger)
		}()

		// Function to get the file mod tinw
		modTimeFn := func() time.Time {
			prevStat, err := os.Stat(f.Name())
			Expect(err).NotTo(HaveOccurred())
			return prevStat.ModTime()
		}

		modTimeNotChangingFnFactory := func(d time.Duration) func() bool {
			mod := modTimeFn()
			timeConsistent := time.Now().Add(d)

			return func() bool {
				lastMod := mod
				mod = modTimeFn()
				if lastMod != mod {
					timeConsistent = time.Now().Add(d)
					return false
				}

				return time.Now().After(timeConsistent)
			}
		}

		readStatusFileFn := func() (*Status, error) {
			return ReadStatusFile(f.Name())
		}

		By("status file should return proper Status object")
		st.SetReady("anykey", false, "reason1")

		Eventually(readStatusFileFn, eventuallyTimeout, eventuallyPoll).ShouldNot(BeNil())
		readSt, err := ReadStatusFile(f.Name())
		Expect(err).NotTo(HaveOccurred())

		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey",
			ConditionStatus{Ready: false, Reason: "reason1"}))
		Expect(readSt.GetReadiness()).To(Equal(false))

		By("status file should be updated when reason changes")
		prevMod := modTimeFn()
		st.SetReady("anykey", false, "reason2")

		// Wait for the file to be updated. Watch for a file update, and then wait until the file is
		// successfully read.
		Eventually(modTimeFn, eventuallyTimeout, eventuallyPoll).ShouldNot(Equal(prevMod))
		Eventually(readStatusFileFn, eventuallyTimeout, eventuallyPoll).ShouldNot(BeNil())
		readSt, err = ReadStatusFile(f.Name())
		Expect(err).NotTo(HaveOccurred())

		// Check the data
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey",
			ConditionStatus{Ready: false, Reason: "reason2"}))
		Expect(readSt.GetReadiness()).To(Equal(false))

		By("status file should be updated when set ready")
		prevMod = modTimeFn()
		st.SetReady("anykey", true, "")

		// Wait for the file to be updated. Watch for a file update, and then wait until the file is
		// successfully read.
		Eventually(modTimeFn, eventuallyTimeout, eventuallyPoll).ShouldNot(Equal(prevMod))
		Eventually(readStatusFileFn, eventuallyTimeout, eventuallyPoll).ShouldNot(BeNil())
		readSt, err = ReadStatusFile(f.Name())
		Expect(err).NotTo(HaveOccurred())

		// Check the data
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey",
			ConditionStatus{Ready: true, Reason: ""}))
		Expect(readSt.GetReadiness()).To(Equal(true))

		By("status file should not be updated when set ready a 2nd time")
		// Set ready again
		prevMod = modTimeFn()
		st.SetReady("anykey", true, "")

		// The file should remain unchanged.
		Consistently(modTimeFn).Should(Equal(prevMod))

		// Make sure the status value has not changed
		readSt, err = ReadStatusFile(f.Name())
		Expect(err).NotTo(HaveOccurred())
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey",
			ConditionStatus{Ready: true, Reason: ""}))
		Expect(readSt.GetReadiness()).To(Equal(true))

		By("status file should be updated to not ready after being ready")
		prevMod = modTimeFn()
		st.SetReady("anykey", false, "reason3")

		// Wait for the file to be updated. Watch for a file update, and then wait until the file is
		// successfully read.
		Eventually(modTimeFn, eventuallyTimeout, eventuallyPoll).ShouldNot(Equal(prevMod))
		Eventually(readStatusFileFn, eventuallyTimeout, eventuallyPoll).ShouldNot(BeNil())
		readSt, err = ReadStatusFile(f.Name())
		Expect(err).NotTo(HaveOccurred())

		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey",
			ConditionStatus{Ready: false, Reason: "reason3"}))
		Expect(readSt.GetReadiness()).To(Equal(false))

		By("status file should handle a lot of concurrent not-ready updates for a lot of keys")
		prevMod = modTimeFn()
		wg := sync.WaitGroup{}
		wg.Add(1000)
		for i := 0; i < 1000; i++ {
			go func(j int) {
				st.SetReady("anykey"+strconv.Itoa(j), false, "reason"+strconv.Itoa(j))
				wg.Done()
			}(i)
		}
		wg.Wait()
		Expect(st.Readiness).To(HaveLen(1001))

		// Wait for the file to be updated and then to remain unchanged.
		Eventually(modTimeFn, eventuallyTimeout, eventuallyPoll).ShouldNot(Equal(prevMod))
		Eventually(modTimeNotChangingFnFactory(time.Second), "3s", "100ms").Should(BeTrue())
		Eventually(readStatusFileFn, eventuallyTimeout, eventuallyPoll).ShouldNot(BeNil())
		readSt, err = ReadStatusFile(f.Name())
		Expect(err).NotTo(HaveOccurred())

		Expect(readSt.Readiness).To(HaveLen(1001))
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey999",
			ConditionStatus{Ready: false, Reason: "reason999"}))
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey500",
			ConditionStatus{Ready: false, Reason: "reason500"}))
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey1",
			ConditionStatus{Ready: false, Reason: "reason1"}))
		Expect(readSt.GetReadiness()).To(Equal(false))

		By("status file should handle a lot of concurrent ready updates for all of the keys (plus more)")
		prevMod = modTimeFn()
		wg = sync.WaitGroup{}
		wg.Add(2000)
		go st.SetReady("anykey", true, "reason")
		for i := 0; i < 2000; i++ {
			go func(j int) {
				st.SetReady("anykey"+strconv.Itoa(j), true, "reason"+strconv.Itoa(j))
				wg.Done()
			}(i)
		}
		wg.Wait()
		Expect(st.Readiness).To(HaveLen(2001))

		// Wait for the file to be updated and then to remain unchanged.
		Eventually(modTimeFn, eventuallyTimeout, eventuallyPoll).ShouldNot(Equal(prevMod))
		Eventually(modTimeNotChangingFnFactory(time.Second), "3s", "100ms").Should(BeTrue())
		Eventually(readStatusFileFn, eventuallyTimeout, eventuallyPoll).ShouldNot(BeNil())
		readSt, err = ReadStatusFile(f.Name())
		Expect(err).NotTo(HaveOccurred())

		Expect(readSt.Readiness).To(HaveLen(2001))
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey1999",
			ConditionStatus{Ready: true, Reason: "reason1999"}))
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey1500",
			ConditionStatus{Ready: true, Reason: "reason1500"}))
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey1000",
			ConditionStatus{Ready: true, Reason: "reason1000"}))
		Expect(readSt.GetReadiness()).To(Equal(true))

		By("status file should handle a lot of concurrent not ready updates for all of the keys (plus more) when the file cannot be written")
		// Tweak the filename to an empty string - this should prevent the file from being written.
		st.statusFile = ""

		// Perform the updates
		prevMod = modTimeFn()
		wg = sync.WaitGroup{}
		wg.Add(3000)
		for i := 0; i < 3000; i++ {
			go func(j int) {
				st.SetReady("anykey"+strconv.Itoa(j), false, "reason"+strconv.Itoa(j))
				wg.Done()
			}(i)
		}
		wg.Wait()
		Expect(st.Readiness).To(HaveLen(3001))

		// The file should remain unchanged.
		Consistently(modTimeFn).Should(Equal(prevMod))

		// Fix the filename (we do this with the lock because the write thread will be busy accessing the data).
		st.readyMutex.Lock()
		st.statusFile = f.Name()
		st.readyMutex.Unlock()

		// Wait for the file to be updated and then to remain unchanged.
		Eventually(modTimeFn, eventuallyTimeout, eventuallyPoll).ShouldNot(Equal(prevMod))
		Eventually(modTimeNotChangingFnFactory(time.Second), "3s", "100ms").Should(BeTrue())
		Eventually(readStatusFileFn, eventuallyTimeout, eventuallyPoll).ShouldNot(BeNil())
		readSt, err = ReadStatusFile(f.Name())
		Expect(err).NotTo(HaveOccurred())

		Expect(readSt.Readiness).To(HaveLen(3001))
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey2999",
			ConditionStatus{Ready: false, Reason: "reason2999"}))
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey1500",
			ConditionStatus{Ready: false, Reason: "reason1500"}))
		Expect(readSt.Readiness).To(HaveKeyWithValue("anykey500",
			ConditionStatus{Ready: false, Reason: "reason500"}))
		Expect(readSt.GetReadiness()).To(Equal(false))
	})
})
