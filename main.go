// Copyright 2017 Tobias Klausmann
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

const (
	golopVersion = "0.2.1"
)

var (
	modeCurrent  = flag.Bool("c", false, "Show current compiles")
	modeEstimate = flag.String("t", "", "Show history of specific package")
	modeHistory  = flag.Bool("e", true, "Show history")
	modeVersion  = flag.Bool("v", false, "Show golop version information and exit")
	logfilename  = flag.String("l", "/var/log/emerge.log", "Location of emerge log to parse.")
)

func main() {
	flag.Parse()

	logfile, err := os.Open(*logfilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not open log file '%s': %s\n", *logfilename, err)
		os.Exit(1)
	}

	if *modeVersion {
		fmt.Printf("golop version %s\n", golopVersion)
		os.Exit(0)
	}

	if *modeCurrent {
		mdus := findMedDurations(logfile)
		curr, err := runningCompiles()
		if err != nil {
			panic(err)
		}
		if len(curr) == 0 {
			fmt.Printf("No compilations currently running.\n")
			os.Exit(0)
		}

		var sts []compileStatus
		longest := 0
		for _, c := range curr {
			status := compileStatus{pkgname: c.pkg, phase: c.phase}
			if len(c.pkg) > longest {
				longest = len(c.pkg)
			}
			n, _ := splitpkgver(status.pkgname)
			md, ok := mdus[n]
			if ok {
				eta := md - time.Since(c.start).Round(time.Second)
				if eta < 0 {
					status.eta = "any time now"
				} else {
					status.eta = eta.String()
				}
			}
			status.elapsed = time.Since(c.start).Round(time.Second).String()
			sts = append(sts, status)
		}
		fmt.Println(tabulate(sts, longest))
		os.Exit(0)
	}

	if *modeEstimate != "" {
		compiles, _, durations := findCompileHist(logfile, nil)
		var filtered []compileHist
		pattern := *modeEstimate

		// We only want the first match of a possible substring, so we replace
		// pattern with the full pkgname on first match and then only compare
		// literally
		firstmatched := false
		for _, compile := range compiles {
			if firstmatched && compile.pkgname == pattern {
				filtered = append(filtered, compile)
				continue
			}
			// We don't have a match yet, compare more generously
			if !firstmatched && pkgnameMatch(compile.pkgname, pattern) {
				filtered = append(filtered, compile)
				pattern = compile.pkgname
				firstmatched = true
			}
		}
		if len(filtered) == 0 {
			fmt.Printf("Found no compilations matching %s\n", *modeEstimate)
			os.Exit(2)
		}

		hists := showHistory(filtered, time.Unix(0, 0))
		// Since we have turned the pattern into the full pkgname, this will
		// always succeed
		durs := durations[pattern]
		meddur := medDuration(durs)

		fmt.Printf("%s\nMedian duration: %+v\n", hists, meddur.Round(time.Second))
		os.Exit(0)
	}

	if *modeHistory {
		compiles, _, _ := findCompileHist(logfile, nil)
		fmt.Printf("%s\n", showHistory(compiles, time.Unix(0, 0)))
		os.Exit(0)
	}

	// If we reach this spot, the user deactivated modeHistory explicitly and
	// did not activate another mode
	flag.Usage()
	os.Exit(1)

}

type compileHist struct {
	start      time.Time
	end        time.Time
	dur        time.Duration
	pkgname    string
	pkgversion string
}

type compileStatus struct {
	pkgname string
	elapsed string
	eta     string
	phase   string
}
