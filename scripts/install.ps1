# install.ps1 — install den (Developer Environment Navigator) with one command.
#
#   irm https://github.com/lukaszgard/den/releases/latest/download/install.ps1 | iex
#
# It finds the latest release (or DEN_VERSION), downloads the zip for this CPU,
# checks it against the release's checksums.txt and puts den.exe in
# %LOCALAPPDATA%\Programs\den (or DEN_INSTALL_DIR), which it adds to your user
# PATH. Works in Windows PowerShell 5.1 and PowerShell 7.
#
# `| iex` cannot pass parameters, so each option has an environment twin:
#   -Version vX.Y.Z   DEN_VERSION      (default: latest)
#   -Dir DIR          DEN_INSTALL_DIR  (default: %LOCALAPPDATA%\Programs\den)
#   -Profile NAME     AWS_PROFILE      (AWS profile for s3 reads)
# Other environment: DEN_INSTALL_BASE (default: GitHub Releases; a mirror laid
# out as latest.json and <version>/<files>, s3:// through the aws CLI or
# https:// with Invoke-WebRequest), DEN_AWS_REGION (default eu-central-1).
#
# Everything sits in functions and the last line runs them: a download cut off
# halfway runs nothing.
param(
    [string]$Version = $env:DEN_VERSION,
    [string]$Dir = $env:DEN_INSTALL_DIR,
    [Alias('Profile')][string]$AwsProfile = $env:AWS_PROFILE
)

# The user PATH lives in the registry; tests define these two themselves.
if (-not (Get-Command Get-DenUserPath -ErrorAction SilentlyContinue)) {
    function Get-DenUserPath { [Environment]::GetEnvironmentVariable('Path', 'User') }
}
if (-not (Get-Command Set-DenUserPath -ErrorAction SilentlyContinue)) {
    function Set-DenUserPath([string]$Value) { [Environment]::SetEnvironmentVariable('Path', $Value, 'User') }
}

function Get-DenArch {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "unsupported CPU '$($env:PROCESSOR_ARCHITECTURE)': den releases cover amd64 and arm64" }
    }
}

# Get-DenUrl SRC: where SRC, a path under the base, is. GitHub Releases keep the
# newest release's files under latest/download and the others under
# download/<tag>; a mirror keeps both as plain paths.
function Get-DenUrl([string]$Base, [string]$Src, [bool]$GitHub) {
    if (-not $GitHub) { return "$Base/$Src" }
    if ($Src -eq 'latest.json') { return "$Base/latest/download/latest.json" }
    return "$Base/download/$Src"
}

# Get-DenFile SRC DEST: SRC is a path under the base.
function Get-DenFile([string]$Base, [string]$Src, [string]$Dest, [string]$AwsProfile, [string]$Region, [bool]$GitHub) {
    if ($Base.StartsWith('s3://')) {
        $a = @('s3', 'cp', "$Base/$Src", $Dest, '--no-progress', '--only-show-errors', '--region', $Region)
        if ($AwsProfile) { $a += @('--profile', $AwsProfile) }
        & aws @a
        if ($LASTEXITCODE -ne 0) {
            $login = 'aws sso login'
            if ($AwsProfile) { $login += " --profile $AwsProfile" }
            throw ("Could not read $Base/$Src.`n" +
                "  - Is the SSO session alive?   $login`n" +
                "  - Does the profile have s3:GetObject on $($Base.Substring(5))/*?`n" +
                "  - Pick a profile with -Profile NAME or `$env:AWS_PROFILE.")
        }
    }
    else {
        if ($Base.StartsWith('https://')) {
            [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        }
        Invoke-WebRequest -UseBasicParsing -Uri (Get-DenUrl $Base $Src $GitHub) -OutFile $Dest
    }
}

function Install-Den([string]$Version, [string]$Dir, [string]$AwsProfile) {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    $github = -not $env:DEN_INSTALL_BASE
    $base = if ($github) { 'https://github.com/lukaszgard/den/releases' } else { $env:DEN_INSTALL_BASE.TrimEnd('/') }
    $region = if ($env:DEN_AWS_REGION) { $env:DEN_AWS_REGION } else { 'eu-central-1' }
    if (-not $Dir) { $Dir = Join-Path $env:LOCALAPPDATA 'Programs\den' }
    $arch = Get-DenArch

    if ($base.StartsWith('s3://') -and -not (Get-Command aws -ErrorAction SilentlyContinue)) {
        throw "the aws CLI (v2) is needed to read ${base}: https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
    }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("den-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        if (-not $Version) {
            $latest = Join-Path $tmp 'latest.json'
            Get-DenFile $base 'latest.json' $latest $AwsProfile $region $github
            $Version = (Get-Content -Raw $latest | ConvertFrom-Json).version
            if (-not $Version) { throw 'latest.json has no version' }
        }
        if (-not $Version.StartsWith('v')) { $Version = "v$Version" }

        # GoReleaser strips the tag's leading v from archive names (update.ArchiveName).
        $name = "den_$($Version.Substring(1))_windows_$arch.zip"
        $zip = Join-Path $tmp $name
        $sums = Join-Path $tmp 'checksums.txt'

        Write-Host "Installing den $Version (windows/$arch) from $base"
        Get-DenFile $base "$Version/$name" $zip $AwsProfile $region $github
        Get-DenFile $base "$Version/checksums.txt" $sums $AwsProfile $region $github

        $want = $null
        foreach ($line in Get-Content $sums) {
            $f = $line -split '\s+', 2
            if ($f.Count -eq 2 -and $f[1].TrimStart('*') -eq $name) { $want = $f[0]; break }
        }
        if (-not $want) { throw "checksums.txt does not list ${name}: nothing installed" }
        $got = (Get-FileHash -Algorithm SHA256 $zip).Hash
        if ($got -ne $want) { throw "checksum mismatch for $name (got $got, want $want): nothing installed" }
        Write-Host 'Checksum ok'

        $x = Join-Path $tmp 'x'
        Expand-Archive -Path $zip -DestinationPath $x
        $new = Join-Path $x 'den.exe'
        if (-not (Test-Path $new)) { throw "$name has no den.exe" }

        New-Item -ItemType Directory -Force -Path $Dir | Out-Null
        $exe = Join-Path $Dir 'den.exe'
        $old = "$exe.old"
        # Windows refuses to overwrite a running exe but allows renaming it: the
        # old file moves aside and goes away now, or on the next run when den
        # is still running.
        Remove-Item -Force -ErrorAction SilentlyContinue $old
        $moved = $false
        if (Test-Path $exe) { Move-Item -Force $exe $old; $moved = $true }
        try {
            Copy-Item -Force $new $exe
        }
        catch {
            if ($moved) { Move-Item -Force $old $exe }
            throw
        }
        Remove-Item -Force -ErrorAction SilentlyContinue $old
        Write-Host "Installed $exe"

        # $IsWindows is undefined in Windows PowerShell 5.1, which only runs on Windows.
        if ($PSVersionTable.PSEdition -ne 'Core' -or $IsWindows) {
            try { & $exe --version } catch { Write-Warning "could not run $exe --version" }
        }

        $userPath = Get-DenUserPath
        $entries = if ($userPath) { $userPath -split ';' | Where-Object { $_ } } else { @() }
        if (-not ($entries | Where-Object { $_.TrimEnd('\') -eq $Dir.TrimEnd('\') })) {
            Set-DenUserPath ((@($entries) + $Dir) -join ';')
            Write-Host ""
            Write-Host "Added $Dir to your user PATH. Open a new terminal to use den."
        }
        else {
            $found = Get-Command den -All -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($found -and $found.Source -ne $exe) {
                Write-Warning "$($found.Source) comes first on your PATH and shadows the den just installed ($exe)"
            }
        }

        Write-Host ""
        Write-Host 'Next:'
        Write-Host '  den doctor                         check the tools den needs'
        $p = if ($AwsProfile) { $AwsProfile } else { '<aws-profile>' }
        Write-Host "  den init --profile $p    write a den.yaml from your AWS account"
    }
    finally {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
    }
}

Install-Den -Version $Version -Dir $Dir -AwsProfile $AwsProfile
