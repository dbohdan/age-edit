complete -c age-edit -s a -l armor -d 'Write an armored age file'
complete -c age-edit -s s -l autosave -d 'Save automatically at regular intervals' -r
complete -c age-edit -l decode -d 'Filter command after decryption, like a decompressor' -r
complete -c age-edit -s e -l editor -d 'Editor command' -r
complete -c age-edit -l encode -d 'Filter command before encryption, like a compressor' -r
complete -c age-edit -s f -l force -d 'Force re-encryption even if the file has not changed'
complete -c age-edit -s L -l no-lock -d 'Do not lock encrypted file'
complete -c age-edit -s M -l no-memlock -d 'Disable mlockall(2) that prevents swapping'
complete -c age-edit -s r -l read-only -d 'Make the temporary file read-only and discard all changes'
complete -c age-edit -s t -l temp-dir -d 'Base temporary directory' -r
complete -c age-edit -s V -l version -d 'Report the program version and exit'
complete -c age-edit -s w -l warn -d 'Warn if the editor exits sooner than expected' -r

# Complete files for both arguments.
complete -c age-edit -n "__fish_is_nth_token 1" -F
complete -c age-edit -n "__fish_is_nth_token 2" -F
