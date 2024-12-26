#! /usr/bin/env fish

cd "$(path dirname "$(status filename)")"

set --local src age-edit.fish
set --local dst $__fish_config_dir/completions/

printf 'copying "%s" to "%s"\n' $src $dst

cp $src $dst
