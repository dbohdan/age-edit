prefix      = /usr/local

exec_prefix = $(prefix)
bindir      = $(exec_prefix)/bin
datarootdir = $(prefix)/share
datadir     = $(datarootdir)
mandir      = $(datarootdir)/man
man1dir     = $(mandir)/man1

INSTALL         = install
INSTALL_PROGRAM = $(INSTALL)
INSTALL_DATA    = $(INSTALL) -m 644

DESTDIR =

install: installdirs
	$(INSTALL_PROGRAM) gpgedit.tcl $(DESTDIR)$(bindir)/gpgedit

installdirs:
	mkdir -p $(DESTDIR)$(bindir)

.PHONY: install installdirs