# age-edit

age-edit is an editor wrapper for files encrypted with [age](https://github.com/FiloSottile/age).

age-edit is designed primarily for Linux and uses `/dev/shm/` by default.
However, it supports and is automatically tested on FreeBSD, macOS, NetBSD, OpenBSD, and Windows.
On those systems, it is up to the user to choose where to create the temporary directory.

## How age-edit works

When you run age-edit with an identities (private keys) file and an encrypted file, it performs the following steps:

1. Decrypt the contents of the age-encrypted file to a temporary file using one of the identities (private keys).
   Optionally, decode after decryption by passing the data through a user-supplied command, like a decompressor.
2. Launch an editor on the temporary file.
   (The default editor is determined by the environment variables `AGE_EDIT_EDITOR`, [`VISUAL`, and `EDITOR`](https://unix.stackexchange.com/questions/4859/visual-vs-editor-what-s-the-difference) with `vi` as a fallback, but it can be any editor, e.g., LibreOffice.)
3. Wait for the editor to exit.
4. Check if the temporary file has been modified by comparing its checksum before and after editing.
   If the file has been modified, proceed, else skip to the next step.
   Encrypt the contents of the temporary file to the encrypted file using public keys derived from the private keys.
   Optionally, encode before encryption by passing the data through a user-supplied command, like a compressor.
   The encrypted file can be "armored": stored as ASCII text in the [PEM](https://en.wikipedia.org/wiki/Privacy-Enhanced_Mail) format.
5. Finally, delete the temporary file.

In other words, age-edit implements
[a](https://wiki.tcl-lang.org/39218)
"[with](https://www.python.org/dev/peps/pep-0343/)"
[pattern](https://clojuredocs.org/clojure.core/with-open).

If any error occurs between steps 4 and 5, age-edit waits for you to press Enter to delete the temporary file.
This gives you an opportunity to save the edited version of the file so you don't lose your edits.

age-edit is beta-quality software.

## Requirements

### Build

- Go 1.22
- Optional: [Task](https://taskfile.dev/) (go-task) 3.28

### Runtime

- Optional: a high limit on locked memory.
  This allows age-edit to use a function that prevents it from being swapped out.
  See the [documentation](https://github.com/dbohdan/pago#memory-locking) for the pago password manager for instructions on configuring the limit.
- Optional: a temporary filesystem mounted on `/dev/shm/`.
  It is usually present on Linux with glibc.

## Installation

### Go

```shell
go install dbohdan.com/age-edit@latest
```

### Nix

An [independent Nix package](https://github.com/dot-file/age-edit) is available for age-edit.

## Usage

<!-- BEGIN USAGE -->
```none
Usage: age-edit [options] [[identities] encrypted]

Arguments:
  identities              identities file path (AGE_EDIT_IDENTITIES_FILE)
  encrypted               encrypted file path (AGE_EDIT_ENCRYPTED_FILE)

Options:
  -a, --armor               write an armored age file (AGE_EDIT_ARMOR)
  -s, --autosave duration   save automatically at regular intervals (0 to
disable, AGE_EDIT_AUTOSAVE)
  -c, --command string      editor command with arguments (overrides the editor
executable, AGE_EDIT_COMMAND)
      --decode string       filter command after decryption, like a decompressor
(AGE_EDIT_DECODE)
  -e, --editor string       editor executable (AGE_EDIT_EDITOR, VISUAL, EDITOR,
default "vi")
      --encode string       filter command before encryption, like a compressor
(AGE_EDIT_ENCODE)
  -f, --force               force re-encryption even if the file hasn't changed
(AGE_EDIT_FORCE)
  -L, --no-lock             do not lock encrypted file (negated AGE_EDIT_LOCK)
  -M, --no-memlock          disable mlockall(2) that prevents swapping (negated
AGE_EDIT_MEMLOCK)
  -r, --read-only           make the temporary file read-only and discard all
changes (AGE_EDIT_READ_ONLY)
  -t, --temp-dir string     base temporary directory (AGE_EDIT_TEMP_DIR, default
"/dev/shm/")
  -V, --version             report the program version and exit
  -w, --warn string         warn if the editor exits sooner than expected
(duration or seconds, 0 to disable, AGE_EDIT_WARN)

An identities file and an encrypted file, given in the arguments or the
environment variables, are required. Default values are read from environment
variables with a built-in fallback. Boolean environment variables accept 0, 1,
true, false, yes, no.
```
<!-- END USAGE -->

The `--editor` option can only specify the editor command to run; it doesn't allow arguments.
Use the `--command` option to specify a command with arguments.

The command string is split into arguments according to the rules of POSIX shell using [anmitsu/go-shlex](https://github.com/anmitsu/go-shlex).
For example, `age-edit --command 'foo --bar "baz 5"'` runs `foo --bar 'baz 5' /path/to/temp-file` to edit the temporary file.

## File locking

age-edit supports file locking to prevent concurrent editing of the same encrypted file.
When file locking is enabled (the default), age-edit locks the encrypted file using [gofrs/flock](https://github.com/gofrs/flock).
If another instance of age-edit has locking enabled and tries to edit the same file, it will fail with an error message that says the file is locked.
This can prevent data loss from multiple copies of age-edit editing the same encrypted file simultaneously.

## Saving without exiting

### Saving on signal

On POSIX systems (BSD, Linux, macOS), you can send the `SIGUSR1` signal to the age-edit process and save changes to the encrypted file without closing the editor.
This is useful for long editing sessions.

```shell
pkill -USR1 age-edit
```

If saving fails, age-edit will ring the [system bell](https://en.wikipedia.org/wiki/Bell_character) and print an error message to standard error.

### Autosave

You can have age-edit automatically save changes at regular intervals without closing the editor.
Use the `-s`/`--autosave` option with a [Go duration string](https://pkg.go.dev/time#ParseDuration) like `30s` (30 seconds), `5m` (5 minutes), or `1h` (1 hour).

```shell
age-edit --autosave 2m identities.txt secrets.txt.age
```

If the duration is invalid or negative, the program will exit with an error.

On POSIX systems, autosave works concurrently with saving on `SIGUSR1`.
They wait for each other.

If an autosave fails (for example, the disk is full), age-edit will ring the system bell and print an error message to standard error.

## Using age-edit with pago

You can use age-edit with a private key stored in [pago](https://github.com/dbohdan/pago) or a similar password manager.
Invoke age-edit like this:

```shell
# Bash
# `pago show secret.key` outputs the private key to stdout.
age-edit -a <(pago show secret.key) secret.txt
```

```fish
# fish shell
# `pago show secret.key` outputs the private key to stdout.
# `--fifo` avoids writing the secret key to a temporary file.
age-edit -a (pago show secret.key | psub --fifo) secret.txt
```

## Editing compressed files

You can use the `--decode` and `--encode` options to apply transformations to the file contents.

The `--decode` option specifies a command to run after decryption to decode or decompress the file.
The `--encode` option specifies a command to run before encryption to encode or compress the file.
Like `--command`, `--decode` and `--encode` are split into arguments according to the rules of POSIX shell.

For example, to use [Zstandard](https://en.wikipedia.org/wiki/Zstd) compression:

```shell
age-edit --decode 'zstd -d' --encode 'zstd -7 --long' id.txt secret.txt.zst.age
```

To compress a previously uncompressed file:

```shell
# No `--decode` option.
age-edit --encode 'zstd -7 --long' ids.txt secret.txt.age
```

Note that unless you use the `--force` option, compression will only be applied if the temporary file changes.

## Forcing re-encryption

The `-f`/`--force` option forces re-encryption of the file even if its contents haven't changed.
This is useful when you want to apply an encoding like compression to a previously unencoded file or to re-encrypt with new identities (as long as at least one can decrypt the file).

For example, to apply Zstandard compression to a file that was previously encrypted without compression:

```shell
age-edit --editor cat --encode zstd --force ids.txt secret.txt.age
```

Without the `--force` option, the encoding would not be applied.

## Security and other considerations

The age identities (private keys) from the identities file are kept in memory while the encrypted file is being edited.
On POSIX systems, the program locks its memory pages using [`mlockall`](https://pubs.opengroup.org/onlinepubs/9799919799/functions/mlockall.html) to prevent being swapped to disk.
The process memory may be saved in unencrypted swap if the system is suspended to disk.
No attempt to prevent the swapping of the process is made on non-POSIX systems like Windows.

The decrypted contents of the file are stored by default in the directory `/dev/shm/age-edit-${username}@${hostname}/abcd0123/`, where `abcd0123` is random.
You can change this to `/custom/path/age-edit-${username}@${hostname}/abcd0123/`.
Other programs run by the same user can access the decrypted file contents.
Note that `/dev/shm/` can be swapped out when swap is enabled.

Temporary files and directories are created with restrictive permissions: 0600 for files and 0700 for directories.
The read-only option sets the file permissions to 0400.

[BLAKE3](https://en.wikipedia.org/wiki/BLAKE3) is used to checksum files.

age-edit doesn't work with multi-document editors.

## License

MIT.
