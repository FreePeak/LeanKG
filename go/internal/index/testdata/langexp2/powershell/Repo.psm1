using module ./Logging.psm1

class Repository {
    [string] $Root

    Repository([string] $root) {
        $this.Root = $root
    }

    [string] Find([string] $name) {
        return Join-Path $this.Root $name
    }
}

function Save-Item {
    param([string]$Path, [string]$Value)
    Set-Content -Path $Path -Value $Value
}
