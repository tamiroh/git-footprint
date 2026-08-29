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

Today `git-footprint` reports, per contributor, every identity in the history
and the EXIF metadata (location, creator, camera) of any image they committed.
Planned:

- real names and internal hostnames leaked in file paths and configs
- more binary-embedded metadata: HEIC/PNG images, `.DS_Store`, PDF and Office
- content PII (addresses, phone numbers, national ID numbers)
