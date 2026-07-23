#Requires -Version 7.4
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string] $ManifestPath,
    [Parameter(Mandatory = $true)]
    [string] $ArtifactPath,
    [Parameter(Mandatory = $true)]
    [string] $ScoopRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (!$IsWindows -or [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne 'X64') {
    throw 'Scoop smoke tests require Windows x86-64'
}

$manifestFile = (Resolve-Path $ManifestPath).Path
$artifactFile = (Resolve-Path $ArtifactPath).Path
$scoopCore = (Resolve-Path $ScoopRoot).Path
$manifest = Get-Content $manifestFile -Raw -Encoding UTF8 | ConvertFrom-Json
$version = [string] $manifest.version
$filename = "open-skills_${version}_windows_amd64.zip"
$canonicalUrl = "https://github.com/EngBlock/open-skills/releases/download/v${version}/${filename}"
$windows = $manifest.architecture.'64bit'
$autoupdateUrl = 'https://github.com/EngBlock/open-skills/releases/download/v$version/open-skills_$version_windows_amd64.zip'
$autoupdateHashes = 'https://github.com/EngBlock/open-skills/releases/download/v$version/checksums.txt'

if ($version -notmatch '^0\.2\.0(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$') {
    throw "manifest version '$version' is not a canonical native 0.2.0 release"
}
$isProduction = $version -ceq '0.2.0'
if ($manifest.description -cnotmatch 'Experimental Windows x86-64') {
    throw 'manifest must label Windows x86-64 experimental'
}
if ($windows.url -cne $canonicalUrl -or $windows.hash -cnotmatch '^[0-9a-f]{64}$') {
    throw 'manifest must contain the canonical Windows archive URL and lowercase SHA-256'
}
if ($manifest.bin -isnot [string] -or $manifest.bin -cne 'open-skills.exe') {
    throw 'manifest must expose only open-skills.exe'
}
if ($manifest.checkver.url -cne 'https://api.github.com/repos/EngBlock/open-skills/releases' -or
    $manifest.autoupdate.architecture.'64bit'.url -cne $autoupdateUrl -or
    $manifest.autoupdate.architecture.'64bit'.hash.url -cne $autoupdateHashes) {
    throw 'manifest must carry canonical Scoop checkver and autoupdate metadata'
}
if ((Get-Content $manifestFile -Raw -Encoding UTF8) -match '(?i)npm|node(?:\.exe)?') {
    throw 'Scoop distribution must not depend on npm or Node.js'
}
if ((Split-Path $artifactFile -Leaf) -cne $filename) {
    throw "artifact must be named $filename"
}
$actualHash = (Get-FileHash $artifactFile -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualHash -cne $windows.hash) {
    throw "artifact checksum $actualHash does not match manifest $($windows.hash)"
}

$validator = Join-Path $scoopCore 'supporting/validator/bin/validator.exe'
& $validator (Join-Path $scoopCore 'schema.json') $manifestFile
if ($LASTEXITCODE -ne 0) {
    throw 'Scoop schema validation failed'
}

$work = Join-Path ([System.IO.Path]::GetTempPath()) "open-skills-scoop-$([Guid]::NewGuid())"
New-Item $work -ItemType Directory | Out-Null
$priorScoop = $env:SCOOP
try {
    $env:SCOOP = Join-Path $work 'scoop-home'
    $installedScoopCore = Join-Path $env:SCOOP 'apps/scoop/current'
    New-Item (Split-Path $installedScoopCore) -ItemType Directory -Force | Out-Null
    New-Item $installedScoopCore -ItemType Junction -Target $scoopCore | Out-Null
    $scoopCore = $installedScoopCore

    $checksums = Join-Path $work 'checksums.txt'
    "$($windows.hash)  $filename`n" | Set-Content $checksums -Encoding utf8NoBOM -NoNewline

    $upgradeManifest = Join-Path $work 'open-skills.json'
    $upgrade = Get-Content $manifestFile -Raw -Encoding UTF8 | ConvertFrom-Json
    $olderVersion = '0.2.0-preview.0'
    $currentPrerelease = 'true'
    $olderPrerelease = 'true'
    if ($isProduction) {
        $olderVersion = '0.1.9'
        $currentPrerelease = 'false'
        $olderPrerelease = 'false'
    }
    $upgrade.version = $olderVersion
    $upgrade.architecture.'64bit'.url = "https://github.com/EngBlock/open-skills/releases/download/v${olderVersion}/open-skills_${olderVersion}_windows_amd64.zip"
    $upgrade.architecture.'64bit'.hash = '0000000000000000000000000000000000000000000000000000000000000000'
    $upgrade.autoupdate.architecture.'64bit'.hash.url = [Uri]::new($checksums, [UriKind]::Absolute).AbsoluteUri
    $releaseIndex = Join-Path $work 'releases.json'
    "[{`"tag_name`":`"v${version}`",`"prerelease`":${currentPrerelease},`"draft`":false},{`"tag_name`":`"v${olderVersion}`",`"prerelease`":${olderPrerelease},`"draft`":false},{`"tag_name`":`"v0.2.0-preview.2`",`"prerelease`":true,`"draft`":false}]" | Set-Content $releaseIndex -Encoding utf8NoBOM
    $upgrade.checkver.url = [Uri]::new($releaseIndex, [UriKind]::Absolute).AbsoluteUri
    $upgrade | ConvertTo-Json -Depth 20 | Set-Content $upgradeManifest -Encoding utf8NoBOM

    & (Join-Path $scoopCore 'bin/checkver.ps1') -App $upgradeManifest -ForceUpdate -ThrowError
    $updated = Get-Content $upgradeManifest -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($updated.version -cne $version -or $updated.architecture.'64bit'.url -cne $canonicalUrl -or $updated.architecture.'64bit'.hash -cne $windows.hash) {
        throw 'Scoop autoupdate did not produce the canonical version, URL, and checksum'
    }

    . (Join-Path $scoopCore 'lib/core.ps1')
    $cacheFile = cache_path 'open-skills' $version $canonicalUrl
    New-Item (Split-Path $cacheFile) -ItemType Directory -Force | Out-Null
    Copy-Item $artifactFile $cacheFile

    & (Join-Path $scoopCore 'bin/scoop.ps1') install $manifestFile
    if ($LASTEXITCODE -ne 0) {
        throw 'Scoop install failed'
    }

    $executable = Join-Path $env:SCOOP 'apps/open-skills/current/open-skills.exe'
    if ((& $executable --version).Trim() -cne $version) {
        throw 'installed open-skills.exe reported the wrong version'
    }
    if ((& $executable --help | Out-String) -notmatch 'Usage:') {
        throw 'installed open-skills.exe help smoke check failed'
    }
    $exposedExecutables = @(Get-ChildItem (Join-Path $env:SCOOP 'shims') -Filter '*.exe' | ForEach-Object Name)
    if ($exposedExecutables.Count -ne 1 -or $exposedExecutables[0] -cne 'open-skills.exe') {
        throw "Scoop exposed unexpected executables: $($exposedExecutables -join ', ')"
    }
} finally {
    $env:SCOOP = $priorScoop
    Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
}
