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

Today `git-footprint` reports, per contributor:

- every author/committer identity in the history
- EXIF metadata (location, creator, camera) of any committed image — JPEG, PNG,
  TIFF, HEIC/AVIF, camera RAW
- the file/folder names a committed `.DS_Store` leaks

Planned:

- real names and internal hostnames leaked in file paths and configs
- PDF and Office document metadata
- content PII (addresses, phone numbers, national ID numbers)
