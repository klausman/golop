# Golop

A pure Go re-implementation of genlop

## Description

On Gentoo systems, [genlop](https://wiki.gentoo.org/wiki/Project:Perl) is the
tool of choice to get information about current and past compilations,
including the history of packages merged, the average time a compile takes and
an estimate when any currently running compilations will finish. The tool is
written in Perl and all of its output is hard-coded. Since it was originally
written at a time when people would rarely have more than two or three
parallel compiles running, it is rather generous with screen real estate. For
example, this is the output of one package compiling:

```
$ genlop -cn

 Currently merging 1 out of 1

 * app-shells/bash-4.4_p12

       current merge time: 3 seconds.
       ETA: less than a minute.
```

On a modern, powerful system, one can easily have 5-10 parallel compilations
happening, which means that it becomes difficult to see what is going on. In
contrast, golop tries to be frugal with screen space:

```
$ golop -c
                package    elapsed ETA
app-shells/bash-4.4_p12         0s 50s
```

Furthermore, genlop does not have stable sorting for its output, which again
makes it harder to see what is going on with several compiles at the same
time. Golop sorts its output in a stable manner.

Finally, on slower systems, or when `emerge.log` is long, genlop can take
several seconds to parse all history. Golop is much faster.

## Limitations

There is one major downside to Golop: it is only as portable as Go, which has
been ported to a lot fewer systems than Perl has. One slight upside of using
Go is that it the binary can simply be copied to machines of a compatible
architecture (and libc), since it is statically linked.

## Known Bugs

Golop does not cope well with multiple instances of `emerge` currently running,
or with several having started around the same time and all but one having
terminated. Doing this right would require a change to the Portage log format.
Future versions of Golop may implement the same heuristics as `genlop` does.

There are currently no tests.

## Building

```
$ go build .
```

## Usage

Golop is not a 100% drop-in for genlop. It has no color output and does not
support all of the information retrieval options genlop has. It still supports
the basic modes of operation genlop has.

```
$ golop -help
Usage of golop:
  -c    Show current compiles
  -e    Show history (default true)
  -l string
        Location of emerge log to parse. (default "/var/log/emerge.log")
  -t string
        Show history of specific package
```

