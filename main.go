package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"dbohdan.com/age-edit/internal/shlex"
	"github.com/carlmjohnson/crockford"
	"github.com/gofrs/flock"
	"github.com/spf13/pflag"
	"lukechampine.com/blake3"
)

const (
	digestSize     = 32
	randomIDLength = 8

	exitOK       = 0
	exitError    = 1
	exitBadUsage = 2

	cliMaxArgs = 2

	defaultTempDirBaseLinux = "/dev/shm/"

	filePerm         = 0o600
	fileReadOnlyPerm = 0o400
	tempDirPerm      = 0o700

	armorEnvVar          = "AGE_EDIT_ARMOR"
	autosaveEnvVar       = "AGE_EDIT_AUTOSAVE"
	decodeEnvVar         = "AGE_EDIT_DECODE"
	encodeEnvVar         = "AGE_EDIT_ENCODE"
	encryptedFileEnvVar  = "AGE_EDIT_ENCRYPTED_FILE"
	forceEnvVar          = "AGE_EDIT_FORCE"
	identitiesFileEnvVar = "AGE_EDIT_IDENTITIES_FILE"
	lockEnvVar           = "AGE_EDIT_LOCK"
	memlockEnvVar        = "AGE_EDIT_MEMLOCK"
	readOnlyEnvVar       = "AGE_EDIT_READ_ONLY"
	tempDirBaseEnvVar    = "AGE_EDIT_TEMP_DIR"
	warnEnvVar           = "AGE_EDIT_WARN"

	version = "0.17.0"
)

var (
	editorEnvVars = []string{"AGE_EDIT_EDITOR", "VISUAL", "EDITOR"}
)

type config struct {
	autosaveInterval time.Duration
	idsPath          string
	encPath          string
	tempDirBase      string

	armor    bool
	force    bool
	lock     bool
	readOnly bool

	command string
	args    []string

	decodeCmd  string
	decodeArgs []string
	encodeCmd  string
	encodeArgs []string
}

type saveError struct {
	err      error
	tempFile string
}

func (e *saveError) Error() string {
	return fmt.Sprintf("encryption failed: %v", e.err)
}

// wrapDecrypt transparently handles both armored and binary age files
// by detecting the armor header and wrapping the reader appropriately
// before decryption.
func wrapDecrypt(r io.Reader, identities ...age.Identity) (io.Reader, error) {
	buffer := make([]byte, len(armor.Header))

	// Check if the input starts with an armor header.
	n, err := io.ReadFull(r, buffer)
	if err != nil && !errors.Is(err, io.EOF) && n < len(armor.Header) {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	armored := string(buffer[:n]) == armor.Header
	r = io.MultiReader(bytes.NewReader(buffer[:n]), r)

	if armored {
		return age.Decrypt(armor.NewReader(r), identities...)
	}

	return age.Decrypt(r, identities...)
}

// withFiles opens input and output files and executes the provided action function,
// ensuring both files are properly closed afterward.
func withFiles(inputPath, outputPath string, action func(in io.Reader, out io.Writer) error) error {
	in, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	return action(in, out)
}

// runFilter executes a command with the given arguments,
// piping input to stdin and output to stdout.
// If cmd is empty, it copies input directly to output.
func runFilter(cmd string, args []string, in io.Reader, out io.Writer) error {
	if strings.TrimSpace(cmd) == "" {
		_, err := io.Copy(out, in)

		return err
	}

	filterCmd := exec.CommandContext(context.Background(), cmd, args...)
	filterCmd.Stdin = in
	filterCmd.Stdout = out
	filterCmd.Stderr = os.Stderr

	return filterCmd.Run()
}

// decryptToFile decrypts inputPath to outputPath,
// optionally applying a decode filter command (e.g., decompressor)
// to the decrypted contents.
func decryptToFile(inputPath, outputPath string, decodeCmd string, decodeArgs []string, identities ...age.Identity) error {
	return withFiles(inputPath, outputPath, func(in io.Reader, out io.Writer) error {
		d, err := wrapDecrypt(in, identities...)
		if err != nil {
			return err
		}

		return runFilter(decodeCmd, decodeArgs, d, out)
	})
}

// encryptToFile encrypts inputPath to outputPath,
// optionally applying an encode filter command (e.g., a compressor)
// before encryption and optionally armoring the output.
func encryptToFile(inputPath, outputPath string, armored bool, encodeCmd string, encodeArgs []string, recipients ...age.Recipient) error {
	return withFiles(inputPath, outputPath, func(in io.Reader, out io.Writer) error {
		w := out

		if armored {
			armorWriter := armor.NewWriter(out)
			defer armorWriter.Close()

			w = armorWriter
		}

		encryptWriter, err := age.Encrypt(w, recipients...)
		if err != nil {
			return err
		}
		defer encryptWriter.Close()

		return runFilter(encodeCmd, encodeArgs, in, encryptWriter)
	})
}

// randomID generates a random 8-character lowercase Crockford-base32-encoded string.
func randomID() string {
	buf := make([]byte, 0, randomIDLength)
	buf = crockford.AppendRandom(crockford.Lower, buf)

	return string(buf)
}

// getRoot removes the ".age" suffix from a path if present.
func getRoot(path string) string {
	return strings.TrimSuffix(path, ".age")
}

// checksumFile computes the BLAKE3 hash of a file.
// If the file does not exist it returns the hash of an empty file.
func checksumFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return the hash of an empty file.
			h := blake3.New(digestSize, nil)

			return h.Sum(nil), nil
		}

		return nil, err
	}
	defer f.Close()

	h := blake3.New(digestSize, nil)
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}

	return h.Sum(nil), nil
}

// checkAccess verifies that a file exists and is readable,
// and if not in read-only mode, also writable.
// It returns true if the file exists, false if it doesn't (and is allowed to be created).
func checkAccess(path string, readOnly bool) (bool, error) {
	_, err := os.Stat(path)

	if err != nil && os.IsNotExist(err) {
		if readOnly {
			return false, fmt.Errorf("%q does not exist; won't attempt to create it in read-only mode", path)
		}

		return false, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return true, fmt.Errorf("can't read from file %q", path)
	}

	f.Close()

	// If not in read-only mode, try to open for writing.
	// We don't want writing to fail later, after the user edits the file.
	if !readOnly {
		f, err := os.OpenFile(path, os.O_RDWR, filePerm)
		if err != nil {
			return true, fmt.Errorf("can't write to file %q", path)
		}

		f.Close()
	}

	return true, nil
}

// loadIdentities parses an identities file.
// It returns both the private identities and their corresponding public recipients.
// Comments and blank lines are ignored.
func loadIdentities(path string) ([]age.Identity, []age.Recipient, error) {
	identityData, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read identities file: %w", err)
	}

	identityCount := 0
	lines := strings.Split(string(identityData), "\n")
	identities := make([]age.Identity, 0, len(lines))
	recipients := make([]age.Recipient, 0, len(lines))

	for _, line := range strings.Split(string(identityData), "\n") {
		line := strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		identityCount++

		identity, err := age.ParseX25519Identity(line)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse private key number %d: %w", identityCount, err)
		}

		identities = append(identities, identity)
		recipients = append(recipients, identity.Recipient())
	}

	if len(identities) == 0 {
		return identities, recipients, errors.New("no identities found in file")
	}

	return identities, recipients, nil
}

// edit implements the edit workflow:
// decrypt the file, launch an editor, detect changes, and re-encrypt if modified.
// It returns the temporary directory path and any error encountered.
// The caller is responsible for cleaning up the temporary directory.
func edit(cfg config) (string, error) {
	exists, err := checkAccess(cfg.encPath, cfg.readOnly)
	if err != nil {
		return "", err
	}

	identities, recipients, err := loadIdentities(cfg.idsPath)
	if err != nil {
		return "", err
	}

	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}

	userDir := fmt.Sprintf("age-edit-%s@%s", currentUser.Username, hostname)
	subdir := randomID()
	tempDir := filepath.Join(cfg.tempDirBase, userDir, subdir)

	err = os.MkdirAll(tempDir, tempDirPerm)
	if err != nil {
		return tempDir, err
	}

	rootname := getRoot(cfg.encPath)
	tempFile := filepath.Join(tempDir, filepath.Base(rootname))

	encLock := flock.New(cfg.encPath)

	//nolint:nestif
	if exists {
		if cfg.lock && !cfg.readOnly {
			locked, err := encLock.TryLock()
			if err != nil {
				return tempDir, fmt.Errorf("failed to acquire lock: %w", err)
			}

			if !locked {
				return tempDir, errors.New("encrypted file is locked")
			}

			defer func() {
				_ = encLock.Unlock()
			}()
		}

		if err := decryptToFile(cfg.encPath, tempFile, cfg.decodeCmd, cfg.decodeArgs, identities...); err != nil {
			return tempDir, err
		}
	}

	beforeSum, err := checksumFile(tempFile)
	if err != nil {
		return tempDir, err
	}

	if cfg.readOnly {
		if err := os.Chmod(tempFile, fileReadOnlyPerm); err != nil {
			return tempDir, err
		}
	}

	var mu sync.Mutex

	saveChanges := func() error {
		mu.Lock()
		defer mu.Unlock()

		currentSum, err := checksumFile(tempFile)
		if err != nil {
			return err
		}

		if cfg.force || !bytes.Equal(beforeSum, currentSum) {
			if err = encryptToFile(tempFile, cfg.encPath, cfg.armor, cfg.encodeCmd, cfg.encodeArgs, recipients...); err != nil {
				return err
			}

			beforeSum = currentSum
		}

		return nil
	}

	if !cfg.readOnly {
		stop := handleSignals(saveChanges)
		defer stop()

		stopAutosave := handleAutosave(cfg.autosaveInterval, saveChanges)
		defer stopAutosave()
	}

	fullArgs := append([]string{}, cfg.args...)
	fullArgs = append(fullArgs, tempFile)

	cmd := exec.CommandContext(context.Background(), cfg.command, fullArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err = cmd.Run(); err != nil {
		return tempDir, err
	}

	if !cfg.readOnly {
		if err := saveChanges(); err != nil {
			return tempDir, &saveError{err: err, tempFile: tempFile}
		}
	}

	return tempDir, nil
}

// parseBool converts a string to a boolean.
// It accepts "1", "true", "yes" as true
// and "0", "false", "no" as false.
// An empty string returns the fallback value.
func parseBool(s string, fallback bool) (bool, error) {
	if s == "" {
		return fallback, nil
	}

	switch strings.ToLower(s) {
	case "1", "true", "yes":
		return true, nil

	case "0", "false", "no":
		return false, nil

	default:
		return false, fmt.Errorf("invalid boolean value: %q", s)
	}
}

// defaultArg retrieves an environment variable.
// It returns the value and a help string indicating this value is the default.
func defaultArg(envVar string) (string, string) {
	value := os.Getenv(envVar)

	helpDefault := ""
	if value != "" {
		helpDefault = fmt.Sprintf(", default %q", value)
	}

	return value, helpDefault
}

// defaultBool retrieves a boolean environment variable, using parseBool to convert it.
// If the variable is not set, the fallback value is returned.
func defaultBool(envVar string, fallback bool) (bool, error) {
	val := os.Getenv(envVar)

	b, err := parseBool(val, fallback)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value for %s: %q", envVar, val)
	}

	return b, nil
}

func defaultArmor() (bool, error) {
	return defaultBool(armorEnvVar, false)
}

func defaultAutosave() (time.Duration, error) {
	val := os.Getenv(autosaveEnvVar)
	if val == "" {
		return 0, nil
	}

	d, err := time.ParseDuration(val)
	if err != nil {
		return 0, fmt.Errorf("invalid duration value for %s: %q", autosaveEnvVar, val)
	}

	return d, nil
}

func defaultDecode() string {
	return os.Getenv(decodeEnvVar)
}

func defaultEncode() string {
	return os.Getenv(encodeEnvVar)
}

// splitCommand splits a command line like a Unix shell.  On Windows the
// backslash is not an escape character, so backslash-separated paths work.
func splitCommand(s string) ([]string, error) {
	if runtime.GOOS == "windows" {
		return shlex.SplitWindows(s)
	}

	return shlex.Split(s, true)
}

func defaultEditor() string {
	for _, envVar := range editorEnvVars {
		value := os.Getenv(envVar)
		if value != "" {
			return value
		}
	}

	return "vi"
}

func defaultForce() (bool, error) {
	return defaultBool(forceEnvVar, false)
}

func defaultLock() (bool, error) {
	return defaultBool(lockEnvVar, true)
}

func defaultMemlock() (bool, error) {
	return defaultBool(memlockEnvVar, true)
}

func defaultReadOnly() (bool, error) {
	return defaultBool(readOnlyEnvVar, false)
}

func defaultTempDirBase() string {
	base := os.Getenv(tempDirBaseEnvVar)
	if base == "" {
		base = defaultTempDirBaseLinux
	}

	return base
}

func defaultWarn() string {
	return os.Getenv(warnEnvVar)
}

// cli parses command-line arguments, validates configuration, and invokes the edit function.
// It returns an appropriate exit code.
func cli() int {
	encryptedFileDefault, encryptedFileHelpDefault := defaultArg(encryptedFileEnvVar)
	identitiesFileDefault, identitiesFileHelpDefault := defaultArg(identitiesFileEnvVar)

	defaultArmorVal, err := defaultArmor()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultAutosaveVal, err := defaultAutosave()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultForceVal, err := defaultForce()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultLockVal, err := defaultLock()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultMemlockVal, err := defaultMemlock()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultReadOnlyVal, err := defaultReadOnly()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	defaultWarnVal := defaultWarn()

	flag := pflag.NewFlagSet("age-edit", pflag.ContinueOnError)

	armored := flag.BoolP(
		"armor",
		"a",
		defaultArmorVal,
		fmt.Sprintf("write an armored age file (%v)", armorEnvVar),
	)
	autosave := flag.DurationP(
		"autosave",
		"s",
		defaultAutosaveVal,
		fmt.Sprintf("save automatically at regular intervals (%v, duration, 0 to disable)", autosaveEnvVar),
	)
	decode := flag.String(
		"decode",
		defaultDecode(),
		fmt.Sprintf("filter command after decryption, like a decompressor (%v)", decodeEnvVar),
	)
	editor := flag.StringP(
		"editor",
		"e",
		defaultEditor(),
		fmt.Sprintf("editor command (%v)", strings.Join(editorEnvVars, ", ")),
	)
	encode := flag.String(
		"encode",
		defaultEncode(),
		fmt.Sprintf("filter command before encryption, like a compressor (%v)", encodeEnvVar),
	)
	force := flag.BoolP(
		"force",
		"f",
		defaultForceVal,
		fmt.Sprintf("force re-encryption even if the file hasn't changed (%v)", forceEnvVar),
	)
	noLock := flag.BoolP(
		"no-lock",
		"L",
		!defaultLockVal,
		fmt.Sprintf("do not lock encrypted file (negated %v)", lockEnvVar),
	)
	noMemlock := flag.BoolP(
		"no-memlock",
		"M",
		!defaultMemlockVal,
		fmt.Sprintf("disable mlockall(2) that prevents swapping (negated %v)", memlockEnvVar),
	)
	readOnly := flag.BoolP(
		"read-only",
		"r",
		defaultReadOnlyVal,
		fmt.Sprintf("make the temporary file read-only and discard all changes (%v)", readOnlyEnvVar),
	)
	showVersion := flag.BoolP(
		"version",
		"V",
		false,
		"report the program version and exit",
	)
	tempDirBase := flag.StringP(
		"temp-dir",
		"t",
		defaultTempDirBase(),
		fmt.Sprintf("base temporary directory (%v)", tempDirBaseEnvVar),
	)
	warn := flag.StringP(
		"warn",
		"w",
		defaultWarnVal,
		fmt.Sprintf("warn if the editor exits sooner than expected (%v, duration or seconds, 0 to disable)", warnEnvVar),
	)

	flag.Usage = func() {
		message := fmt.Sprintf(
			`Usage: %s [options] [[identities] encrypted]

Arguments:
  identities                identities file path (%s%s)
  encrypted                 encrypted file path (%s%s)

Options:
%s
An identities file and an encrypted file, given in the arguments or the environment variables, are required. Default values are read from environment variables with a built-in fallback. Boolean environment variables accept 0, 1, true, false, yes, no.
`,
			filepath.Base(os.Args[0]),
			identitiesFileEnvVar,
			identitiesFileHelpDefault,
			encryptedFileEnvVar,
			encryptedFileHelpDefault,
			// Merge "(default ...)" with our own parentheticals.
			strings.ReplaceAll(flag.FlagUsages(), ") (", ", "),
		)

		fmt.Fprint(os.Stderr, message)
	}
	if err := flag.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return exitOK
		}

		fmt.Fprintln(os.Stderr, "Error:", err)

		return exitBadUsage
	}

	if *showVersion {
		fmt.Println(version)

		return exitOK
	}

	if flag.NArg() > cliMaxArgs {
		fmt.Fprintln(
			os.Stderr,
			"Error: too many arguments",
		)

		return exitBadUsage
	}

	cfg := config{
		autosaveInterval: *autosave,
		idsPath:          identitiesFileDefault,
		encPath:          encryptedFileDefault,
		tempDirBase:      *tempDirBase,

		armor:    *armored,
		force:    *force,
		lock:     !*noLock,
		readOnly: *readOnly,

		command: "",
		args:    []string{},

		decodeCmd:  "",
		decodeArgs: []string{},
		encodeCmd:  "",
		encodeArgs: []string{},
	}

	//nolint:mnd
	if flag.NArg() == 1 {
		cfg.encPath = flag.Arg(0)
	} else if flag.NArg() == 2 {
		cfg.idsPath = flag.Arg(0)
		cfg.encPath = flag.Arg(1)
	}

	if cfg.encPath == "" || cfg.idsPath == "" {
		fmt.Fprintln(
			os.Stderr,
			"Error: need an identities file and an encrypted file",
		)

		return exitBadUsage
	}

	if !*noMemlock {
		if err := lockMemory(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v. You may need to increase the limit on locked memory. Pass --no-memlock to suppress this error.\n", err)

			return exitError
		}
	}

	args, err := splitCommand(*editor)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: failed to split editor command")
		os.Exit(exitBadUsage)
	}

	cfg.command = args[0]
	cfg.args = args[1:]

	if *decode != "" {
		args, err := splitCommand(*decode)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: failed to split decode command")
			os.Exit(exitBadUsage)
		}

		cfg.decodeCmd = args[0]
		cfg.decodeArgs = args[1:]
	}

	if *encode != "" {
		args, err := splitCommand(*encode)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: failed to split encode command")
			os.Exit(exitBadUsage)
		}

		cfg.encodeCmd = args[0]
		cfg.encodeArgs = args[1:]
	}

	var warnDuration time.Duration

	if *warn != "" {
		d, err := time.ParseDuration(*warn)
		if err != nil {
			seconds, errInt := strconv.Atoi(*warn)
			if errInt != nil {
				fmt.Fprintln(os.Stderr, "Error: invalid duration for --warn:", *warn)

				return exitBadUsage
			}

			d = time.Duration(seconds) * time.Second
		}

		warnDuration = d
	}

	start := time.Now()

	tempDir, err := edit(cfg)
	if tempDir != "" {
		// Remove the "age-edit-..." directory if empty
		// after removing the temporary file and the random subdirectory.
		defer os.Remove(filepath.Dir(tempDir))
		defer os.RemoveAll(tempDir)
	}

	if warnDuration > 0 && time.Since(start) <= warnDuration {
		fmt.Fprintf(
			os.Stderr,
			"Warning: editor exited after less than %v\n",
			warnDuration,
		)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)

		var saveErr *saveError
		if errors.As(err, &saveErr) {
			fmt.Fprintf(
				os.Stderr,
				"Press <Enter> to delete temporary file %q\n",
				saveErr.tempFile,
			)

			_, _ = fmt.Scanln()
		}

		return exitError
	}

	return exitOK
}

func main() {
	os.Exit(cli())
}
