#!/usr/bin/env tclsh
package require Tcl 8.6
package require cmdline
package require fileutil

namespace eval ::gpgedit {
    variable gpgPath gpg2
    variable commandPrefix [list -ignorestderr -- \
            $gpgPath --batch --yes --passphrase-fd 0]
}

proc ::gpgedit::decrypt {in out passphrase} {
    variable commandPrefix
    exec {*}$commandPrefix --decrypt -o $out $in << $passphrase
}

proc ::gpgedit::encrypt {in out passphrase} {
    variable commandPrefix
    exec {*}$commandPrefix --symmetric --armor -o $out $in << $passphrase
}

proc ::gpgedit::edit {encrypted editor {readOnly 0}} {
    # Return code and result.
    set code ok
    set result {}

    puts Passphrase:

    if {$::tcl_platform(platform) eq {unix}} {
        set oldMode [exec stty -g <@ stdin]
        exec stty -echo <@ stdin
    }
    gets stdin passphrase
    if {$::tcl_platform(platform) eq {unix}} {
        exec stty {*}$oldMode <@ stdin
    }

    try {
        if {[file extension $encrypted] in {.asc .gpg}} {
            set rootname [file rootname $encrypted]
        } else {
            set rootname $encrypted
        }
        set extension [file extension $rootname]
        close [file tempfile temporary $extension]

        file attributes $temporary -permissions 0600
        if {[file exists $encrypted]} {
            decrypt $encrypted $temporary $passphrase
        }
        exec $editor $temporary <@ stdin >@ stdout 2>@ stderr
        if {!$readOnly} {
            encrypt $temporary $encrypted $passphrase
        }
    } on error message {
        puts "Error: $message"
        puts "Press <enter> to delete the temporary file $temporary."
        gets stdin
        set code error
        set result $message
    } finally {
        file delete $temporary
    }
    return -code $code $result
}

proc ::gpgedit::main {argv0 argv} {
    set options {
        {editor.arg  {}  {editor to use}}
        {ro              {read-only mode -- all changes will be lost}}
        {warn.arg    0   {warn if the editor exits after less than X\
                          seconds}}
    }
    set usage "$argv0 \[options] filename ...\noptions:"
    if {[catch {set opts [::cmdline::getoptions argv $options $usage]}] \
            || ([set filename [lindex $argv 0]] eq {})} {
        puts -nonewline [::cmdline::usage $options $usage]
        exit 1
    }

    # The argument -editor.
    if {[dict get $opts editor] ne {}} {
        set editor [dict get $opts editor]
    } else {
        set editor $::env(EDITOR)
    }

    # The argument -warn.
    set warn [dict get $opts warn]
    if {![string is double -strict $warn]} {
        puts "Error: the argument to -warn must be a number."
        exit 1
    }
    if {$warn < 0} {
        puts "Error: the argument to -warn can't be negative."
        exit 1
    }
    if {$warn > 0} {
        set t [clock seconds]
    }

    try {
        edit $filename $editor [dict get $opts ro]
    } on error _ {
        # Do nothing.
    } on ok _ {
        if {($warn > 0) && ([clock seconds] - $t <= 1000 * $warn)} {
            puts "Warning: the editor exited after less than $warn second(s)."
        }
    }
}

# If this is the main script...
if {[info exists argv0] && ([file tail [info script]] eq [file tail $argv0])} {
    ::gpgedit::main $argv0 $argv
}