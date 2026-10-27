# Changelog

## 0.1.1

- Declare `engines.dbx: >=0.6.55` and Host API version 1 in the manifest.
- Add Linux x64/arm64 and macOS x64/arm64 packages alongside Windows x64.
- Use a portable Go packager with checksums and Unix executable modes.
- Test extracted packages on five matching native CI runners before release upload.
- Document why the declarative workbench needs no `host.workbench` permission.
- Preserve previously published v0.1.0 artifacts and their store signing.

## 0.1.0

- Initial Windows x64 BSON codec, Redis read-only preview and conversion workbench.
- Optional custom external-program conversion.
