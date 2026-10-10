# install_test.ps1 — tests of install.ps1 against a folder laid out like the
# release bucket, with `aws` stubbed by a function and the user PATH kept in a
# variable. The zips hold a stub den.exe (a text file: it is not run here).
# Run with `make script-test` (needs pwsh).
$ErrorActionPreference = 'Stop'

$global:install = Join-Path $PSScriptRoot 'install.ps1'
$global:work = Join-Path ([IO.Path]::GetTempPath()) ("den-install-test-" + [Guid]::NewGuid().ToString('N'))
$global:bucket = Join-Path $work 'bucket'
New-Item -ItemType Directory -Path $bucket | Out-Null
$global:failed = 0

function Fail([string]$msg) {
    [Console]::Error.WriteLine("FAIL $msg")
    $global:failed++
}

# --- stubs -------------------------------------------------------------

$global:awsLog = [System.Collections.Generic.List[string]]::new()
$global:awsFail = $false
function aws {
    $global:awsLog.Add(($args -join ' '))
    if ($global:awsFail) { $global:LASTEXITCODE = 1; return }
    $key = $args[2] -replace '^s3://acme-releases/den/', ''
    $src = Join-Path $global:bucket $key
    if (-not (Test-Path $src)) { $global:LASTEXITCODE = 1; return }
    Copy-Item $src $args[3]
    $global:LASTEXITCODE = 0
}

$global:userPath = ''
function Get-DenUserPath { $global:userPath }
function Set-DenUserPath([string]$Value) { $global:userPath = $Value }

# --- the bucket --------------------------------------------------------

# New-Release VERSION: <bucket>/vVERSION with a zip per CPU whose den.exe says
# "den VERSION <arch>", plus checksums.txt.
function New-Release([string]$Ver) {
    $rel = Join-Path $bucket "v$Ver"
    New-Item -ItemType Directory -Path $rel | Out-Null
    $lines = @()
    foreach ($arch in 'amd64', 'arm64') {
        $stage = Join-Path $work "stage-$Ver-$arch"
        New-Item -ItemType Directory -Path $stage | Out-Null
        Set-Content -NoNewline -Path (Join-Path $stage 'den.exe') -Value "den $Ver $arch"
        Set-Content -Path (Join-Path $stage 'README.md') -Value 'readme'
        $zip = Join-Path $rel "den_${Ver}_windows_$arch.zip"
        Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip
        $lines += "$((Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower())  den_${Ver}_windows_$arch.zip"
    }
    Set-Content -Path (Join-Path $rel 'checksums.txt') -Value $lines
}

function Set-Latest([string]$Ver) {
    Set-Content -Path (Join-Path $bucket 'latest.json') -Value "{`"version`":`"v$Ver`",`"notes`":`"a\nb`"}"
}

# Invoke-Install ARGS…: runs install.ps1 in a child scope; sets $global:out to
# its output and $global:err to the terminating error (or $null).
function Invoke-Install {
    $global:awsLog.Clear()
    $global:err = $null
    try { $global:out = (& $global:install @args *>&1 | Out-String) }
    catch { $global:err = $_.Exception.Message; $global:out = '' }
}

function Expect-Ok([string]$name) { if ($global:err) { Fail "${name}: failed: $($global:err)" } }
function Expect-Error([string]$name, [string]$pattern) {
    if (-not $global:err) { Fail "${name}: no error" }
    elseif ($global:err -notlike $pattern) { Fail "${name}: error '$($global:err)' lacks '$pattern'" }
}
function Expect-Exe([string]$name, [string]$dir, [string]$want) {
    $exe = Join-Path $dir 'den.exe'
    if (-not (Test-Path $exe)) { Fail "${name}: $exe missing"; return }
    $got = Get-Content -Raw $exe
    if ($got -ne $want) { Fail "${name}: den.exe says '$got', want '$want'" }
}

try {
    New-Release '0.1.0'
    New-Release '0.2.0'
    Set-Latest '0.2.0'

    $env:PROCESSOR_ARCHITECTURE = 'AMD64'
    $env:LOCALAPPDATA = Join-Path $work 'local'
    foreach ($v in 'DEN_VERSION', 'DEN_INSTALL_DIR', 'DEN_INSTALL_BASE', 'DEN_AWS_REGION', 'AWS_PROFILE') {
        Remove-Item "Env:$v" -ErrorAction SilentlyContinue
    }
    # The bucket tests read a mirror; the GitHub one clears this.
    $env:DEN_INSTALL_BASE = 's3://acme-releases/den'

    # --- latest, into the default folder, user PATH updated ------------
    $dir = Join-Path $env:LOCALAPPDATA 'Programs\den'
    Invoke-Install
    Expect-Ok 'latest'
    Expect-Exe 'latest' $dir 'den 0.2.0 amd64'
    if ($out -notlike '*Checksum ok*') { Fail "latest: no 'Checksum ok': $out" }
    if (Test-Path (Join-Path $dir 'README.md')) { Fail 'latest: extracted more than den.exe' }
    if ($userPath -ne $dir) { Fail "latest: user PATH is '$userPath', want '$dir'" }
    if ($out -notlike '*den init --profile <aws-profile>*') { Fail "latest: no next steps: $out" }

    # --- a second run: replaced, PATH not doubled, no .old left -------
    Invoke-Install -Version v0.1.0
    Expect-Ok 'reinstall'
    Expect-Exe 'reinstall' $dir 'den 0.1.0 amd64'
    if ($userPath -ne $dir) { Fail "reinstall: user PATH is '$userPath'" }
    if (Test-Path (Join-Path $dir 'den.exe.old')) { Fail 'reinstall: den.exe.old left behind' }
    if ($awsLog -match 'latest.json') { Fail 'reinstall: read latest.json despite -Version' }

    # --- PATH keeps other entries, appends ours, ignores a trailing \ --
    $global:userPath = 'C:\Tools;C:\Other'
    $dir2 = Join-Path $work 'elsewhere'
    Invoke-Install -Dir $dir2
    Expect-Ok 'path append'
    if ($userPath -ne "C:\Tools;C:\Other;$dir2") { Fail "path append: '$userPath'" }
    $global:userPath = "C:\Tools;$dir2\"
    Invoke-Install -Dir $dir2
    if ($userPath -ne "C:\Tools;$dir2\") { Fail "path with trailing slash changed: '$userPath'" }

    # --- env twins ------------------------------------------------------
    $global:userPath = ''
    $env:DEN_VERSION = '0.1.0'
    $env:DEN_INSTALL_DIR = Join-Path $work 'envdir'
    $env:AWS_PROFILE = 'from-env'
    $env:DEN_AWS_REGION = 'eu-west-1'
    Invoke-Install
    Expect-Ok 'env'
    Expect-Exe 'env' $env:DEN_INSTALL_DIR 'den 0.1.0 amd64'
    if (-not ($awsLog -match '--profile from-env')) { Fail "env: aws got no --profile: $awsLog" }
    if (-not ($awsLog -match '--region eu-west-1')) { Fail "env: aws got another region: $awsLog" }
    if ($out -notlike '*den init --profile from-env*') { Fail "env: next steps lack the profile: $out" }
    foreach ($v in 'DEN_VERSION', 'DEN_INSTALL_DIR', 'AWS_PROFILE', 'DEN_AWS_REGION') { Remove-Item "Env:$v" }

    # --- ARM64 ----------------------------------------------------------
    $env:PROCESSOR_ARCHITECTURE = 'ARM64'
    $dir3 = Join-Path $work 'arm'
    Invoke-Install -Dir $dir3
    Expect-Exe 'arm64' $dir3 'den 0.2.0 arm64'
    $env:PROCESSOR_ARCHITECTURE = 'x86'
    Invoke-Install -Dir (Join-Path $work 'x86')
    Expect-Error 'unsupported CPU' '*unsupported CPU*'
    $env:PROCESSOR_ARCHITECTURE = 'AMD64'

    # --- a checksum mismatch installs nothing --------------------------
    New-Release '0.3.0'
    Add-Content -Path (Join-Path $bucket 'v0.3.0\den_0.3.0_windows_amd64.zip') -Value 'tampered'
    $dir4 = Join-Path $work 'mismatch'
    Invoke-Install -Version v0.3.0 -Dir $dir4
    Expect-Error 'mismatch' '*checksum mismatch*'
    if (Test-Path (Join-Path $dir4 'den.exe')) { Fail 'mismatch: installed anyway' }
    # …and leaves an installed den alone
    New-Item -ItemType Directory -Path $dir4 | Out-Null
    Set-Content -NoNewline -Path (Join-Path $dir4 'den.exe') -Value 'old'
    Invoke-Install -Version v0.3.0 -Dir $dir4
    Expect-Exe 'mismatch keeps old' $dir4 'old'

    # --- an archive missing from checksums.txt -------------------------
    New-Release '0.4.0'
    $sums = Join-Path $bucket 'v0.4.0\checksums.txt'
    Set-Content -Path $sums -Value (Get-Content $sums | Where-Object { $_ -notmatch 'amd64' })
    $dir5 = Join-Path $work 'unlisted'
    Invoke-Install -Version v0.4.0 -Dir $dir5
    Expect-Error 'not listed' '*does not list den_0.4.0_windows_amd64.zip*'
    if (Test-Path (Join-Path $dir5 'den.exe')) { Fail 'not listed: installed anyway' }

    # --- GitHub Releases are the default --------------------------------
    $global:webLog = [System.Collections.Generic.List[string]]::new()
    function global:Invoke-WebRequest {
        param([switch]$UseBasicParsing, [string]$Uri, [string]$OutFile)
        $global:webLog.Add($Uri)
        $key = $Uri -replace '^https://github.com/lukaszgard/den/releases/(latest/)?download/', ''
        $src = Join-Path $global:bucket $key
        if (-not (Test-Path $src)) { throw "404 $Uri" }
        Copy-Item $src $OutFile
    }
    Remove-Item Env:DEN_INSTALL_BASE
    $dir6 = Join-Path $work 'github'
    Invoke-Install -Dir $dir6
    Expect-Ok 'github'
    Expect-Exe 'github' $dir6 'den 0.2.0 amd64'
    if ($webLog -notcontains 'https://github.com/lukaszgard/den/releases/latest/download/latest.json') { Fail "github: latest.json not read from latest/download: $webLog" }
    if ($webLog -notcontains 'https://github.com/lukaszgard/den/releases/download/v0.2.0/den_0.2.0_windows_amd64.zip') { Fail "github: archive not read from download/<tag>: $webLog" }
    Remove-Item Function:\Invoke-WebRequest
    $env:DEN_INSTALL_BASE = 's3://acme-releases/den'

    # --- aws failure says what to try ----------------------------------
    $global:awsFail = $true
    Invoke-Install -Dir (Join-Path $work 'awsfail') -Profile acme-dev
    Expect-Error 'aws failure' '*aws sso login --profile acme-dev*'
    Expect-Error 'aws failure' '*s3:GetObject*'
    $global:awsFail = $false

    # --- a version that is not there -----------------------------------
    Invoke-Install -Version v9.9.9 -Dir (Join-Path $work 'nover')
    Expect-Error 'missing version' '*Could not read*v9.9.9*'
}
finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $work
}

if ($failed -ne 0) {
    [Console]::Error.WriteLine("$failed install.ps1 test(s) failed")
    exit 1
}
Write-Host 'install.ps1 tests passed'
