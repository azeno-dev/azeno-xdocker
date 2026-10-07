# xDocker for SynoCommunity (spksrc)

These files are copied into a fork of https://github.com/SynoCommunity/spksrc
for the pull request described in PLAN.md section 12. They have **not** been
built inside spksrc yet; expect review changes.

```
cp -R cross/xdocker <spksrc>/cross/
cp -R spk/xdocker   <spksrc>/spk/
cd <spksrc>
make setup                          # once
cd spk/xdocker && make arch-x64-7.2 # builds packages/xdocker_x64-7.2_<ver>.spk
```

The source tarball must already contain the built web UI
(`backend/internal/web/dist`), because spksrc does not run npm. The release
workflow (`.github/workflows/release.yml`) publishes it as
`xdocker-<version>-src.tar.gz`; update `PKG_VERS` and `digests` for each
release (`make digests` in `cross/xdocker`).
