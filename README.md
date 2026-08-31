# git-footprint

Check what your git history reveals about you before you make a repository public.

## Install

```sh
go install github.com/tamiroh/git-footprint@latest
```

Needs Go 1.24+ and `git`.

## Usage

```sh
git-footprint [--no-color] [--color] [--no-pager] [--fail-on LEVEL] [--version] [REPO]
```

`REPO` defaults to the current directory.

## Roadmap

Today `git-footprint` reports, per contributor:

- every author/committer identity in the history
- embedded metadata (location, creator, camera, software, creation date) of any
  committed image (JPEG, PNG, TIFF, HEIC/AVIF, camera RAW), video (MP4, MOV) or
  PDF
- the file/folder names a committed `.DS_Store` leaks

Planned:

- real names and internal hostnames leaked in file paths and configs
- Office document metadata
- content PII (addresses, phone numbers, national ID numbers)
