function Get-User {
    param([int]$Id)
    return @{ id = $Id; name = "user$Id" }
}

function Set-User {
    param([int]$Id, [string]$Name)
    return @{ id = $Id; name = $Name }
}

function Test-Helper {
    Write-Host "helper"
}