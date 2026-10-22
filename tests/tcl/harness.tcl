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
array set ::cmd_pass {}
array set ::cmd_fail {}
array set ::cmd_skip {}
set ::cmd_failed_tests {}

# Per-command report key: the uppercase command token the test name starts with
# ("EXPIREAT - ..." -> EXPIREAT); the one unnamed case maps to its subject.
proc command_of {name} {
    set first [lindex [split $name] 0]
    set first [string trim $first ":-"]
    if {$first ne "" && [string is upper -strict $first]} {
        return $first
    }
    if {$name eq "No negative zero"} {
        return "INCRBYFLOAT"
    }
    return "OTHER"
}
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
    set cmd [command_of $name]
    if {[string match "*external:skip*" $tags] || [string match "*needs:repl*" $tags] || [string match "*needs:debug*" $tags]} {
        incr ::num_skipped
        if {![info exists ::cmd_skip($cmd)]} { set ::cmd_skip($cmd) 0 }
        incr ::cmd_skip($cmd)
        return
    }
    incr ::num_tests
    set start [clock milliseconds]
    if {[catch {uplevel 1 $code} retval error]} {
        incr ::num_failed
        if {![info exists ::cmd_fail($cmd)]} { set ::cmd_fail($cmd) 0 }
        incr ::cmd_fail($cmd)
        lappend ::cmd_failed_tests $name
        puts "FAIL $name"
        puts "     $error"
        return
    }
    if {$okpattern ne "undefined" && $okpattern ne $retval && ![string match $okpattern $retval]} {
        incr ::num_failed
        if {![info exists ::cmd_fail($cmd)]} { set ::cmd_fail($cmd) 0 }
        incr ::cmd_fail($cmd)
        lappend ::cmd_failed_tests $name
        puts "FAIL $name"
        puts "     expected '$okpattern', got '$retval'"
        return
    }
    if {![info exists ::cmd_pass($cmd)]} { set ::cmd_pass($cmd) 0 }
    incr ::cmd_pass($cmd)
    incr ::num_passed
    puts "ok   $name ([expr {[clock milliseconds]-$start}] ms)"
}

source [file join $root $suite]
$::client close
puts "----"
set all_commands [lsort [concat [array names ::cmd_pass] [array names ::cmd_fail] [array names ::cmd_skip]]]
foreach cmd $all_commands {
    set passed 0
    set failed 0
    set skipped 0
    if {[info exists ::cmd_pass($cmd)]} { set passed $::cmd_pass($cmd) }
    if {[info exists ::cmd_fail($cmd)]} { set failed $::cmd_fail($cmd) }
    if {[info exists ::cmd_skip($cmd)]} { set skipped $::cmd_skip($cmd) }
    puts "REPORT $cmd passed=$passed failed=$failed skipped=$skipped"
}
puts "passed $::num_passed, failed $::num_failed, skipped $::num_skipped, total $::num_tests"
if {$::num_failed > 0} { exit 1 }
