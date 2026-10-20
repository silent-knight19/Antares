# Thin Tcl harness for selected Redis 7.4.11 unit suites.
#
# Redis test code is downloaded at run time by run-expire.sh and is never
# vendored in this MIT repository. The harness supplies only the minimum
# start_server/tags/test glue needed by the selected tests, plus the official
# Redis Tcl client and assertion helpers.

set root [lindex $argv 0]
set port [lindex $argv 1]
set onlyfile [lindex $argv 2]
set suite [lindex $argv 3]

set ::curfile $suite
set ::verbose 0
set ::valgrind 0
set ::only_tests {}
if {[file exists $onlyfile]} {
    set fp [open $onlyfile r]
    set ::only_tests [split [string trim [read $fp]] "\n"]
    close $fp
}

source [file join $root tests/support/redis.tcl]
source [file join $root tests/support/test.tcl]
source [file join $root tests/support/util.tcl]
set ::client [redis 127.0.0.1 $port]

proc r {args} { uplevel 1 [list $::client {*}$args] }

proc start_server {options code} {
    array set opt $options
    set tags {}
    if {[info exists opt(tags)]} { set tags [string map {\" ""} $opt(tags)] }
    if {[lsearch -exact $tags external:skip] >= 0 || [lsearch -exact $tags needs:repl] >= 0} {
        return
    }
    uplevel 1 $code
}

proc start_cluster {args} {}

proc tags {taglist code} { uplevel 1 $code }

proc test {name code {okpattern undefined} {tags {}}} {
    if {[llength $::only_tests] > 0 && ![search_pattern_list $name $::only_tests]} {
        incr ::num_skipped
        return
    }
    if {[string match "*external:skip*" $tags] || [string match "*needs:repl*" $tags] || [string match "*needs:debug*" $tags]} {
        incr ::num_skipped
        return
    }
    incr ::num_tests
    set start [clock milliseconds]
    if {[catch {uplevel 1 $code} retval error]} {
        incr ::num_failed
        puts "FAIL $name"
        puts "     $error"
        return
    }
    if {$okpattern ne "undefined" && $okpattern ne $retval && ![string match $okpattern $retval]} {
        incr ::num_failed
        puts "FAIL $name"
        puts "     expected '$okpattern', got '$retval'"
        return
    }
    incr ::num_passed
    puts "ok   $name ([expr {[clock milliseconds]-$start}] ms)"
}

source [file join $root $suite]
$::client close
puts "----"
puts "passed $::num_passed, failed $::num_failed, skipped $::num_skipped, total $::num_tests"
if {$::num_failed > 0} { exit 1 }
