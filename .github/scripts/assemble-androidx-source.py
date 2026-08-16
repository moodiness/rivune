#!/usr/bin/env python3
"""Bundle exact AndroidX release trees used by an Android runtime inventory."""

from __future__ import annotations

import pathlib
import shutil
import subprocess
import sys
import zipfile

SUPPORT_REPOSITORY = "https://android.googlesource.com/platform/frameworks/support"
SUPPORT_BUNDLE = "androidx-support-repository"
MEDIA3_TREE = "androidx-media3-1.11.0"
MEDIA3_COMMIT = "2bc207851df311340767e913931ca7b28cab1794"

MEDIA3_RELEASES = {
    "androidx.media3:media3-common:1.11.0",
    "androidx.media3:media3-container:1.11.0",
    "androidx.media3:media3-database:1.11.0",
    "androidx.media3:media3-datasource:1.11.0",
    "androidx.media3:media3-decoder:1.11.0",
    "androidx.media3:media3-exoplayer-dash:1.11.0",
    "androidx.media3:media3-exoplayer-hls:1.11.0",
    "androidx.media3:media3-exoplayer:1.11.0",
    "androidx.media3:media3-extractor:1.11.0",
    "androidx.media3:media3-ui:1.11.0",
}
# Exact coordinates are deliberate: a version change must fail closed until its
# official release range endpoint is reviewed and added here.
SUPPORT_RELEASES = {
    "androidx.activity:activity-compose:1.10.1": ("261165aaaecdddde7e136be4f706bb9f5e1b8b34", "official Activity 1.10.1 release range endpoint"),
    "androidx.activity:activity-ktx:1.10.1": ("261165aaaecdddde7e136be4f706bb9f5e1b8b34", "official Activity 1.10.1 release range endpoint"),
    "androidx.activity:activity:1.10.1": ("261165aaaecdddde7e136be4f706bb9f5e1b8b34", "official Activity 1.10.1 release range endpoint"),
    "androidx.annotation:annotation-experimental:1.4.1": ("cb993e8594b035615bb12e0cb8294769eb7c10d1", "official Annotation Experimental 1.4.1 release range endpoint"),
    "androidx.annotation:annotation-jvm:1.9.1": ("87b88ad088cc9b18d4ab75611fc8b74e8b01c24a", "official Annotation 1.9.1 release range endpoint"),
    "androidx.appcompat:appcompat-resources:1.6.1": ("481dd991240c3469339ec7b93fc74dcabd4f6656", "official AppCompat 1.6.1 release range endpoint"),
    "androidx.arch.core:core-common:2.2.0": ("d00300b06c00dbf348f871980400948cdf7b10dc", "official Arch Core 2.2.0 release range endpoint"),
    "androidx.arch.core:core-runtime:2.2.0": ("d00300b06c00dbf348f871980400948cdf7b10dc", "official Arch Core 2.2.0 release range endpoint"),
    "androidx.autofill:autofill:1.0.0": ("34328d238b2999f231e824d2a9149acdbe3d5c00", "official Autofill 1.0.0 release range endpoint"),
    "androidx.collection:collection-jvm:1.5.0": ("fb376ee6d05727045f41e320bd36923eb385c5f8", "official Collection 1.5.0 release range endpoint"),
    "androidx.collection:collection-ktx:1.5.0": ("fb376ee6d05727045f41e320bd36923eb385c5f8", "official Collection 1.5.0 release range endpoint"),
    "androidx.compose.animation:animation-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.animation:animation-core-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.foundation:foundation-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.foundation:foundation-layout-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.material3:material3-android:1.3.2": ("a8c757d3d74ce0f52c23b8562468fbcc09a7e62f", "official Compose Material3 1.3.2 release range endpoint"),
    "androidx.compose.material:material-icons-core-android:1.7.8": ("215cdfd8cb9c0762dd0347c383250644057c367f", "official Compose Material 1.7.8 release range endpoint"),
    "androidx.compose.material:material-icons-extended-android:1.7.8": ("215cdfd8cb9c0762dd0347c383250644057c367f", "official Compose Material 1.7.8 release range endpoint"),
    "androidx.compose.material:material-ripple-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.runtime:runtime-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.runtime:runtime-saveable-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.ui:ui-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.ui:ui-geometry-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.ui:ui-graphics-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.ui:ui-text-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.ui:ui-tooling-preview-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.ui:ui-unit-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.compose.ui:ui-util-android:1.8.2": ("754896be0859599f16ed264d79a04ee337bac777", "official Compose 1.8.2 release range endpoint"),
    "androidx.concurrent:concurrent-futures:1.2.0": ("f3f60b391b39a0d5c6bbb3bb9f83f227a0c8a72b", "official Concurrent 1.2.0 release range endpoint"),
    "androidx.core:core-ktx:1.13.1": ("3be294a272164cf1920219d3e09cbabfefeb1de6", "official Core 1.13.1 release range endpoint"),
    "androidx.core:core-viewtree:1.0.0": ("d70c42c692a5ce230394b651ac975fc7d03519c8", "official Core ViewTree beta endpoint; RC and 1.0.0 stable unchanged"),
    "androidx.core:core:1.13.1": ("3be294a272164cf1920219d3e09cbabfefeb1de6", "official Core 1.13.1 release range endpoint"),
    "androidx.customview:customview-poolingcontainer:1.0.0": ("1e0793130863c72dc4a2d02bc975128f3ef0158b", "official PoolingContainer 1.0.0 release range endpoint"),
    "androidx.customview:customview:1.0.0": ("50a39caa72955aae0c75225fd9805ab537cbf049", "official lower boundary of first post-1.0 CustomView release range"),
    "androidx.emoji2:emoji2:1.4.0": ("2d25c5e925564cf1f52fb6d2f88088d2dd5becfe", "official Emoji2 1.4.0 release range endpoint"),
    "androidx.exifinterface:exifinterface:1.3.7": ("90d685e3591d448bbd5ebdaab90653d87ae3d91e", "official ExifInterface 1.3.7 release range endpoint"),
    "androidx.graphics:graphics-path:1.0.1": ("8a05a22af450d589ef911d772a001a49dcb05b71", "official Graphics Path 1.0.1 release source endpoint"),
    "androidx.interpolator:interpolator:1.0.0": ("3cf783a59b758889e17fc7482bea76a7cde800ce", "last full tree before 2018-09-21 release; published Java sources byte-matched"),
    "androidx.lifecycle:lifecycle-common-java8:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-common-jvm:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-livedata-core:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-process:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-runtime-android:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-runtime-compose-android:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-runtime-ktx-android:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-viewmodel-android:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-viewmodel-compose-android:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-viewmodel-ktx:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.lifecycle:lifecycle-viewmodel-savedstate-android:2.9.0": ("ac0ac96d77785db18140c9bad8f1de95f2418806", "official Lifecycle 2.9.0 release range endpoint"),
    "androidx.profileinstaller:profileinstaller:1.4.0": ("3bc0fb3beaccef509bb170cd69ddaad907b493b8", "official ProfileInstaller 1.4.0 release range endpoint"),
    "androidx.recyclerview:recyclerview:1.3.0": ("86bf1b167c3ff33791a75eb42b0c8f056eaeab3e", "official RecyclerView 1.3.0 release range endpoint"),
    "androidx.savedstate:savedstate-android:1.3.0": ("5b67d17950276ae45c2b89c55904a019de4b2041", "official SavedState 1.3.0 release range endpoint"),
    "androidx.savedstate:savedstate-ktx:1.3.0": ("5b67d17950276ae45c2b89c55904a019de4b2041", "official SavedState 1.3.0 release range endpoint"),
    "androidx.startup:startup-runtime:1.1.1": ("8337e0c05174a73ea6db825b680f461c33144bf4", "official Startup 1.1.1 release range endpoint"),
    "androidx.tracing:tracing:1.0.0": ("2a518b53bb3172e4945cba723ae628e9c18bcd23", "official Tracing 1.0.0 release range endpoint"),
    "androidx.vectordrawable:vectordrawable-animated:1.1.0": ("7e8073001f8db1bc9e0ff39615c67f390a6a6420", "official VectorDrawable 1.1.0 release range endpoint"),
    "androidx.vectordrawable:vectordrawable:1.1.0": ("7e8073001f8db1bc9e0ff39615c67f390a6a6420", "official VectorDrawable 1.1.0 release range endpoint"),
    "androidx.versionedparcelable:versionedparcelable:1.1.1": ("9fd278801e06c07a5d230fd7edbb97e16c322949", "official VersionedParcelable 1.1.1 release range endpoint"),
}


def run(*args: str, stdout=None) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(args, check=True, stdout=stdout)


def verify_interpolator(repository: pathlib.Path, source_root: pathlib.Path) -> None:
    coordinate = "androidx.interpolator:interpolator:1.0.0"
    commit = SUPPORT_RELEASES[coordinate][0]
    source_jar = source_root / "androidx.interpolator/interpolator/1.0.0/interpolator-1.0.0-sources.jar"
    prefix = "androidx/interpolator/view/animation/"
    expected = sorted(prefix + name for name in (
        "FastOutLinearInInterpolator.java", "FastOutSlowInInterpolator.java",
        "LinearOutSlowInInterpolator.java", "LookupTableInterpolator.java"))
    with zipfile.ZipFile(source_jar) as archive:
        published = sorted(name for name in archive.namelist() if name.endswith(".java"))
        if published != expected:
            raise SystemExit("Interpolator published source set changed")
        for name in published:
            upstream = run(
                "git", "-C", str(repository), "show",
                f"{commit}:interpolator/src/main/java/{name}",
                stdout=subprocess.PIPE,
            ).stdout
            if archive.read(name) != upstream:
                raise SystemExit(f"Interpolator source differs from pinned support tree: {name}")


def main() -> None:
    if len(sys.argv) != 4:
        raise SystemExit("usage: assemble-androidx-source.py INVENTORY JVM_SOURCE_ROOT FULL_UPSTREAMS")
    inventory_path, source_root, upstream_root = map(pathlib.Path, sys.argv[1:])
    inventory = [line.split("\t", 1)[0] for line in inventory_path.read_text(encoding="utf-8").splitlines()]
    expected = [coordinate for coordinate in inventory if coordinate.startswith("androidx.")]
    media3 = {coordinate for coordinate in expected if coordinate.startswith("androidx.media3:")}
    if media3 != MEDIA3_RELEASES:
        missing = sorted(MEDIA3_RELEASES - media3)
        extra = sorted(media3 - MEDIA3_RELEASES)
        raise SystemExit(f"Media3 full-source mapping differs from runtime inventory; missing={missing}, extra={extra}")
    support = set(expected) - media3
    if support != set(SUPPORT_RELEASES):
        missing = sorted(support - set(SUPPORT_RELEASES))
        extra = sorted(set(SUPPORT_RELEASES) - support)
        raise SystemExit(f"AndroidX support mapping differs from runtime inventory; missing={missing}, extra={extra}")

    rows: list[tuple[str, str, str, str]] = []
    for coordinate in expected:
        if coordinate in media3:
            rows.append((coordinate, MEDIA3_TREE, MEDIA3_COMMIT, "full tagged source and build tree"))
        else:
            commit, provenance = SUPPORT_RELEASES[coordinate]
            rows.append((coordinate, SUPPORT_BUNDLE, commit, provenance))
    manifest = source_root / "ANDROIDX_COORDINATES.tsv"
    manifest.write_text("".join("\t".join(row) + "\n" for row in rows), encoding="utf-8")

    staging_repository = source_root.parent / ".androidx-support.git"
    repository = upstream_root / SUPPORT_BUNDLE
    shutil.rmtree(staging_repository, ignore_errors=True)
    shutil.rmtree(repository, ignore_errors=True)
    run("git", "init", "-q", "--bare", str(staging_repository))
    run("git", "-C", str(staging_repository), "remote", "add", "origin", SUPPORT_REPOSITORY)
    commits = sorted({value[0] for value in SUPPORT_RELEASES.values()})
    refs = [f"refs/heads/androidx-source/{commit}" for commit in commits]
    refspecs = [f"{commit}:{ref}" for commit, ref in zip(commits, refs)]
    # Depth one retains each complete release tree but no unrelated history. Git's
    # shared object store deduplicates identical source/build blobs across revisions.
    run("git", "-C", str(staging_repository), "fetch", "-q", "--depth=1", "--no-tags", "origin", *refspecs)
    for commit, ref in zip(commits, refs):
        resolved = run("git", "-C", str(staging_repository), "rev-parse", f"{ref}^{{commit}}", stdout=subprocess.PIPE).stdout.decode().strip()
        if resolved != commit:
            raise SystemExit(f"AndroidX source endpoint did not resolve to {commit}")
    run("git", "-C", str(staging_repository), "fsck", "--full", "--no-dangling")
    verify_interpolator(staging_repository, source_root)
    # Repack the exact shallow refs single-threaded, then remove acquisition-only
    # metadata. The archived bare repository remains directly cloneable offline.
    run("git", "-C", str(staging_repository), "-c", "pack.threads=1", "repack", "-ad", "--depth=50", "--window=250")
    for path in ("FETCH_HEAD", "config", "description"):
        (staging_repository / path).unlink(missing_ok=True)
    shutil.rmtree(staging_repository / "hooks", ignore_errors=True)
    (staging_repository / "config").write_text(
        "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = true\n",
        encoding="ascii",
    )
    shutil.move(staging_repository, repository)

    checkout = source_root / "checkout-androidx-support.sh"
    checkout.write_text(
        "#!/bin/sh\nset -eu\n"
        "if [ \"$#\" -ne 2 ]; then echo \"usage: $0 COORDINATE DESTINATION\" >&2; exit 2; fi\n"
        "root=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\n"
        f"commit=$(awk -F '\\t' -v coordinate=\"$1\" '$1 == coordinate && $2 == \"{SUPPORT_BUNDLE}\" {{ print $3 }}' \"$root/ANDROIDX_COORDINATES.tsv\")\n"
        "[ -n \"$commit\" ] || { echo \"unknown bundled AndroidX coordinate: $1\" >&2; exit 1; }\n"
        f"git clone \"$root/full-upstreams/{SUPPORT_BUNDLE}\" \"$2\"\n"
        "git -C \"$2\" checkout --detach \"$commit\"\n",
        encoding="utf-8",
    )
    checkout.chmod(0o755)


if __name__ == "__main__":
    main()
