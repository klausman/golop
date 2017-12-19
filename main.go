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
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	NANOSPERSEC = 1E9
)

var (
	COMPILE_START_RE    *regexp.Regexp
	COMPILE_COMPLETE_RE *regexp.Regexp
	UNMERGE_START_RE    *regexp.Regexp
	FIRST_PACKAGE_RE    *regexp.Regexp
	modeCurrent         = flag.Bool("c", false, "Show current compiles")
	modeHistory         = flag.Bool("e", true, "Show history")
	modeEstimate        = flag.String("t", "", "Show history of specific package")
	logfilename         = flag.String("l", "/var/log/emerge.log", "Location of emerge log to parse.")
)

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
}

type ByPkgname []compileStatus

func (n ByPkgname) Len() int           { return len(n) }
func (n ByPkgname) Swap(i, j int)      { n[i], n[j] = n[j], n[i] }
func (n ByPkgname) Less(i, j int) bool { return n[i].pkgname < n[j].pkgname }

func init() {
	common_re := `\((?P<ith>\d+) of (?P<total>\d+)\) (?P<package>[A-Za-z0-9/_-]+)-(?P<version>\d[^ ]+) to /`
	COMPILE_START_RE = regexp.MustCompile(`>>> emerge ` + common_re)
	COMPILE_COMPLETE_RE = regexp.MustCompile(`::: completed emerge ` + common_re)
	UNMERGE_START_RE = regexp.MustCompile(`=== Unmerging... \((?P<package>[A-Za-z0-9\/_-]+)-(?P<version>\d.*)\)`)
	// A heuristic for portage restarting with --keep-going
	FIRST_PACKAGE_RE = regexp.MustCompile(`>>> emerge \(1 of`)
}

func main() {
	flag.Parse()

	logfile, err := os.Open(*logfilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not open log file '%s': %s\n", *logfilename, err)
		os.Exit(1)
	}

	if *modeCurrent {
		_, running, durations := parselog(logfile)
		curr, longest := showCurrent(running, durations)
		if curr == nil {
			fmt.Printf("No compilations currently running.\n")
			os.Exit(0)
		}
		fmt.Printf("%s\n", tabulate(curr, longest))
		os.Exit(0)
	}

	if *modeEstimate != "" {
		compiles, _, durations := parselog(logfile)
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
		avgdur := avgDuration(durs)

		fmt.Printf("%s\nAverage duration: %+v\n", hists, avgdur.Round(NANOSPERSEC))
		os.Exit(0)
	}

	if *modeHistory {
		compiles, _, _ := parselog(logfile)
		fmt.Printf("%s\n", showHistory(compiles, time.Unix(0, 0)))
		os.Exit(0)
	}

	// If we reach this spot, the user deactivated modeHistory explicitly and
	// did not activate another mode
	flag.Usage()
	os.Exit(1)

}

func showCurrent(curr map[string]compileHist,
	history map[string][]time.Duration) ([]compileStatus, int) {
	longest := 0
	if len(curr) == 0 {
		return nil, 0
	}
	var ret []compileStatus
	for pkgver, data := range curr {
		var etas string
		elapsed := time.Now().Sub(data.start)
		hist, ok := history[data.pkgname]
		if !ok {
			etas = "unknown"
		} else {
			eta := avgDuration(hist) - elapsed
			// Round to seconds
			if eta.Seconds() < 0 {
				etas = "any time now"
			} else {
				etas = fmt.Sprintf("%+v", eta.Round(NANOSPERSEC))
			}
		}
		if len(pkgver) > longest {
			longest = len(pkgver)
		}
		ret = append(ret, compileStatus{
			pkgname: pkgver,
			elapsed: fmt.Sprintf("%+v", elapsed.Round(NANOSPERSEC)),
			eta:     etas,
		})

	}
	sort.Sort(ByPkgname(ret))
	return ret, longest
}

func showHistory(compiles []compileHist, start time.Time) string {
	var ret []string
	var shown int
	for _, compile := range compiles {
		if compile.start.UnixNano() >= start.UnixNano() {
			shown += 1
			ret = append(ret,
				fmt.Sprintf("%s: %s-%s: %+v",
					compile.start.Format(time.RFC3339), compile.pkgname, compile.pkgversion,
					compile.dur.Round(NANOSPERSEC)))
		}
	}
	ret = append(ret,
		fmt.Sprintf("Total number of compilations: %d", shown))
	return strings.Join(ret, "\n")
}

func getReMatches(re *regexp.Regexp, tomatch string) map[string]string {
	m := re.FindStringSubmatch(tomatch)
	ret := make(map[string]string)
	for i, name := range re.SubexpNames() {
		if i != 0 {
			ret[name] = m[i]
		}
	}
	return ret
}

func parselog(fd *os.File) ([]compileHist, map[string]compileHist, map[string][]time.Duration) {
	var lineno int
	var compiles []compileHist

	durations := make(map[string][]time.Duration)
	inprogress := make(map[string]compileHist)
	// Unmerge history is not completely implemented yet
	unmerges := make(map[string]compileHist)

	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		line := scanner.Text()
		lineno++
		fields := strings.Fields(line)
		if len(fields) < 2 {
			// A line we can't parse: we'll have to just ignore it
			continue
		}
		message := strings.Join(fields[1:], " ")
		ts, err := strconv.ParseInt(fields[0][:len(fields[0])-1], 10, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not parse timestamp on line %d: %+v\n", lineno, err)
		}
		dt := time.Unix(ts, 0)
		if message == "*** exiting successfully." ||
			message == "*** terminating." ||
			FIRST_PACKAGE_RE.MatchString(message) {
			// discard all open sessions
			inprogress = make(map[string]compileHist)
			//unmerges = make(map[string]compileHist)
		}

		if COMPILE_START_RE.MatchString(message) {
			values := getReMatches(COMPILE_START_RE, message)
			c := compileHist{
				start:      dt,
				pkgname:    values["package"],
				pkgversion: values["version"],
			}
			inprogress[fmt.Sprintf("%v-%v", values["package"], values["version"])] = c
			continue
		}
		if UNMERGE_START_RE.MatchString(message) {
			values := getReMatches(UNMERGE_START_RE, message)
			c := compileHist{
				start:      dt,
				pkgname:    values["package"],
				pkgversion: values["version"],
			}
			unmerges[fmt.Sprintf("%v-%v", values["package"], values["version"])] = c
			continue
		}
		if COMPILE_COMPLETE_RE.MatchString(message) {
			values := getReMatches(COMPILE_COMPLETE_RE, message)
			pkgver := fmt.Sprintf("%v-%v", values["package"], values["version"])
			c, ok := inprogress[pkgver]
			if !ok {
				if c, ok = unmerges[pkgver]; ok {
					delete(unmerges, pkgver)
				}
				continue
			}
			c.end = dt
			c.dur = c.end.Sub(c.start)
			compiles = append(compiles, c)
			durations[c.pkgname] = append(durations[c.pkgname], c.dur)
			delete(inprogress, pkgver)
			continue
		}
	}
	return compiles, inprogress, durations
}

func avgDuration(durs []time.Duration) time.Duration {
	totaltime := time.Duration(0)
	for _, t := range durs {
		totaltime += t
	}
	return time.Duration(int64(totaltime) / int64(len(durs)))
}

func pkgnameMatch(pkgname, pattern string) bool {
	if pkgname == pattern {
		return true
	}
	components := strings.Split(pkgname, "/")
	if len(components) != 2 {
		// This should never happen, but let's be defensive
		return false
	}
	if components[1] == pattern {
		return true
	}
	return false
}

func tabulate(p []compileStatus, longest int) string {
	var out []string
	tmpl := fmt.Sprintf("%%%ds %%10s %%-s", longest)
	out = append(out, fmt.Sprintf(tmpl, "Package", "elapsed", "ETA"))
	for _, c := range p {
		out = append(out, fmt.Sprintf(tmpl, c.pkgname, c.elapsed, c.eta))
	}
	return strings.Join(out, "\n")
}
