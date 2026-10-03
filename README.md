# git-footprint

Check your repository for identifying information in commit identities and file metadata.

## Install

```sh
go install github.com/tamiroh/git-footprint@latest
```

Needs Go 1.24+ and `git`. The binary lands on your `PATH` as `git-footprint`,
so git picks it up as a subcommand.

## Usage

```sh
git footprint [--no-color] [--color] [--no-pager] [--fail-on LEVEL] [--version] [REPO]
```

`REPO` defaults to the current directory.

## Features

`git footprint` reports, per contributor:

- every author/committer identity in the history
- embedded metadata (location, creator, camera, software, creation date) of
  supported committed images (JPEG, PNG, WebP, GIF, TIFF, camera RAW), videos (MP4, MOV),
  PDFs, Office documents (`.docx`, `.xlsx`, `.pptx`) and fonts (`.ttf`, `.otf`,
  `.ttc`, `.woff`), including those inside ZIP, tar and tar.gz archives
- the file/folder names a committed `.DS_Store` leaks
  (including inside ZIP, tar and tar.gz archives)
- owner names stored in tar and tar.gz archive headers

## Roadmap

- real names and internal hostnames leaked in file paths and configs
- content PII (addresses, phone numbers, national ID numbers)
