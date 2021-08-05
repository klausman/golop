// Copyright 2019 Tobias Klausmann
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
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	compileStartRegEx    *regexp.Regexp
	compileCompleteRegEx *regexp.Regexp
	unmergeStartRegEx    *regexp.Regexp
	splitpkgverRegEx     *regexp.Regexp
	latestStart          map[string]int64

	procDir = flag.String("d", "/proc", "Root of /proc filesystem")
)

func init() {
	commonRegEx := `\((?P<ith>\d+) of (?P<total>\d+)\) (?P<package>[A-Za-z0-9/_-]+)-(?P<version>\d[^ ]+) to /`
	compileStartRegEx = regexp.MustCompile(`>>> emerge ` + commonRegEx)
	compileCompleteRegEx = regexp.MustCompile(`::: completed emerge ` + commonRegEx)
	unmergeStartRegEx = regexp.MustCompile(`=== Unmerging... \((?P<package>[A-Za-z0-9\/_-]+)-(?P<version>\d.*)\)`)
	splitpkgverRegEx = regexp.MustCompile(`(?P<package>[A-Za-z0-9/_-]+)-(?P<version>\d[^ ]+)`)
	latestStart = make(map[string]int64)
}

func showHistory(compiles []compileHist, start time.Time) string {
	var ret []string
	var shown int
	for _, compile := range compiles {
		if compile.start.UnixNano() >= start.UnixNano() {
			shown++
			ret = append(ret,
				fmt.Sprintf("%s: %s-%s: %+v",
					compile.start.Format(time.RFC3339), compile.pkgname, compile.pkgversion,
					compile.dur.Round(time.Second)))
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

func findMedDurations(fd *os.File) map[string]time.Duration {
	var lineno int

	durations := make(map[string][]time.Duration)
	inprogress := make(map[string]compileHist)
	pkg2md := make(map[string]time.Duration)

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
			continue
		}
		dt := time.Unix(ts, 0)
		if compileStartRegEx.MatchString(message) {
			pv, ch := extractEmStart(message, dt)
			inprogress[pv] = ch
			p, _ := splitpkgver(pv)
			latestStart[p] = ts
			continue
		}
		if compileCompleteRegEx.MatchString(message) {
			pv := extractComplete(message)
			c, ok := inprogress[pv]
			if !ok {
				continue
			}
			c.end = dt
			c.dur = c.end.Sub(c.start)
			durations[c.pkgname] = append(durations[c.pkgname], c.dur)
			delete(inprogress, pv)
			continue
		}
	}
	for pn, durs := range durations {
		pkg2md[pn] = medDuration(durs)
	}
	return pkg2md
}

func extractEmStart(message string, dt time.Time) (string, compileHist) {
	values := getReMatches(compileStartRegEx, message)
	c := compileHist{
		start:      dt,
		pkgname:    values["package"],
		pkgversion: values["version"],
	}
	return fmt.Sprintf("%s-%s", values["package"], values["version"]), c
}

func extractComplete(message string) string {
	values := getReMatches(compileCompleteRegEx, message)
	return fmt.Sprintf("%v-%v", values["package"], values["version"])
}

func medDuration(durs sortableDurs) time.Duration {
	sort.Sort(durs)
	if len(durs)%2 != 0 {
		// And odd number of elements there is a definite middle element
		return durs[len(durs)/2]
	}
	// An even number of elements means we need to average the midmodst pair
	mph := len(durs) / 2
	mpl := mph - 1
	return durs[(mpl+mph)/2]
}

type sortableDurs []time.Duration

func (d sortableDurs) Len() int           { return len(d) }
func (d sortableDurs) Less(i, j int) bool { return d[i] < d[j] }
func (d sortableDurs) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }

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
	tmpl := fmt.Sprintf("%%%ds %%10s %%10s %%-s", longest)
	out = append(out, fmt.Sprintf(tmpl, "Package", "Phase", "Elapsed", "ETA"))
	for _, c := range p {
		out = append(out, fmt.Sprintf(tmpl, c.pkgname, c.phase, c.elapsed, c.eta))
	}
	return strings.Join(out, "\n")
}

func runningCompiles() ([]runningCompile, error) {
	var currpkgs []runningCompile
	pl, err := processes(*procDir)
	if err != nil {
		return nil, err
	}
	for _, p := range pl {
		if len(p.Cmdline) > 1 &&
			strings.HasPrefix(p.Cmdline[0], "[") &&
			strings.HasSuffix(p.Cmdline[0], "sandbox") {

			var s time.Time
			pkg := strings.Split(p.Cmdline[0][1:], "]")[0]
			tok := strings.Split(p.Cmdline[len(p.Cmdline)-1], " ")
			phase := tok[len(tok)-1]
			p, _ := splitpkgver(pkg)
			if ct, ok := latestStart[p]; ok {
				s = time.Unix(ct, 0)
			} else {
				continue
			}
			currpkgs = append(currpkgs, runningCompile{pkg: pkg, start: s, phase: phase})
		}
	}
	return currpkgs, nil
}

type runningCompile struct {
	pkg   string
	start time.Time
	phase string
}

func splitpkgver(pv string) (string, string) {
	values := getReMatches(splitpkgverRegEx, pv)
	return values["package"], values["version"]
}

func findCompileHist(fd *os.File, running map[string]bool) ([]compileHist, map[string]compileHist, map[string][]time.Duration) {
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
		if compileStartRegEx.MatchString(message) {
			values := getReMatches(compileStartRegEx, message)
			c := compileHist{
				start:      dt,
				pkgname:    values["package"],
				pkgversion: values["version"],
			}
			inprogress[fmt.Sprintf("%v-%v", values["package"], values["version"])] = c
			continue
		}
		if unmergeStartRegEx.MatchString(message) {
			values := getReMatches(unmergeStartRegEx, message)
			c := compileHist{
				start:      dt,
				pkgname:    values["package"],
				pkgversion: values["version"],
			}
			unmerges[fmt.Sprintf("%v-%v", values["package"], values["version"])] = c
			continue
		}
		if compileCompleteRegEx.MatchString(message) {
			values := getReMatches(compileCompleteRegEx, message)
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
	nip := make(map[string]compileHist)
	for k, v := range inprogress {
		if running[k] {
			nip[k] = v
		}
	}
	return compiles, nip, durations
}

func processes(dirname string) ([]process, error) {
	var ps []process
	entries, err := ioutil.ReadDir(dirname)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		fp := path.Join(dirname, entry.Name())
		fi, err := os.Stat(fp)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			var p process
			p.PID, err = strconv.ParseUint(entry.Name(), 10, 64)
			if err == nil {
				data, err := ioutil.ReadFile(path.Join(fp, "cmdline"))
				if err != nil {
					continue
				}
				p.Cmdline = dropEmptyStr(strings.Split(string(data), "\000"))
				ps = append(ps, p)
			}
		}

	}
	return ps, nil
}

type process struct {
	PID     uint64
	Cmdline []string
}

func dropEmptyStr(ss []string) []string {
	var r []string
	for _, s := range ss {
		if s != "" {
			r = append(r, s)
		}
	}
	return r
}
