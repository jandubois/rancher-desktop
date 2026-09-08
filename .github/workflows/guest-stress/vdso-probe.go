// vdso-probe tests whether the guest's vDSO clock path is corrupted under
// the hypervisor. It runs one goroutine per CPU in one of two modes:
//
//	clock:   a tight loop calling time.Now(), which on linux/amd64 reads the
//	         clock through the vDSO. It flags any reading that goes backward
//	         or jumps more than a second, and a corrupted vDSO also makes the
//	         process fault outright.
//	noclock: an identical tight loop doing pure integer work and never
//	         touching the clock, as the control.
//
// Only the main goroutine reads the clock in noclock mode (one time.Sleep to
// bound the run), so the worker hot loops differ in exactly one thing: the
// clock call. A clock-mode fault or anomaly with a clean noclock run points
// the corruption at the vDSO.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	mode := flag.String("mode", "clock", "clock or noclock")
	seconds := flag.Int("seconds", 300, "how long to run")
	flag.Parse()

	workers := runtime.NumCPU()
	var stop int32
	var iters, anomalies, checksum uint64
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local, anom, sum uint64
			switch *mode {
			case "clock":
				prev := time.Now()
				for atomic.LoadInt32(&stop) == 0 {
					now := time.Now()
					d := now.Sub(prev)
					if d < 0 || d > time.Second {
						anom++
						fmt.Printf("anomaly: prev=%d now=%d delta=%v\n",
							prev.UnixNano(), now.UnixNano(), d)
					}
					prev = now
					local++
				}
			case "noclock":
				var x uint64 = 1
				for atomic.LoadInt32(&stop) == 0 {
					x = x*6364136223846793005 + 1442695040888963407
					sum += x
					local++
				}
			default:
				fmt.Fprintln(os.Stderr, "mode must be clock or noclock")
				os.Exit(2)
			}
			atomic.AddUint64(&iters, local)
			atomic.AddUint64(&anomalies, anom)
			atomic.AddUint64(&checksum, sum)
		}()
	}

	time.Sleep(time.Duration(*seconds) * time.Second)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()

	fmt.Printf("mode=%s workers=%d iters=%d anomalies=%d checksum=%d\n",
		*mode, workers, iters, anomalies, checksum)
	if anomalies > 0 {
		os.Exit(3)
	}
}
