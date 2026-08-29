# git-footprint

Check what your git history reveals about you before you make a repository public.

## Install

```sh
go install github.com/tamiroh/git-footprint@latest
```

Needs Go 1.23+ and `git`.

## Usage

```sh
git-footprint [--no-color] [--version] [REPO]
```

`REPO` defaults to the current directory.

## Roadmap

Today `git-footprint` reports the **identity footprint** only. Planned:

- real names and internal hostnames leaked in file paths and configs
- image EXIF and other binary-embedded metadata (via `exiftool`)
- content PII (addresses, phone numbers, national ID numbers)
