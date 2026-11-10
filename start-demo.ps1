# Este script abre um terminal separado para cada serviço, deixando os logs visíveis.

# $PSScriptRoot é a pasta onde este próprio script está salvo.
$projectFolder = $PSScriptRoot
$binaryPath = Join-Path $projectFolder "mini-cdn.exe"

# Se esta sessão ainda tiver o PATH antigo, procura o Go no diretório padrão do Windows.
$goExecutable = (Get-Command "go.exe" -ErrorAction SilentlyContinue).Source
if (-not $goExecutable) {
    $goExecutable = Join-Path $env:ProgramFiles "Go\bin\go.exe"
}
if (-not (Test-Path -LiteralPath $goExecutable)) {
    throw "Go não encontrado. Instale Go ou abra um terminal novo e tente novamente."
}

# O projeto é compilado uma vez; as cinco janelas reutilizam o mesmo executável.
Push-Location $projectFolder
try {
    & $goExecutable build -o $binaryPath .
    if ($LASTEXITCODE -ne 0) {
        throw "A compilação falhou. Confira as mensagens acima."
    }
}
finally {
    Pop-Location
}

# startServiceWindow monta o comando e abre uma nova janela do PowerShell.
function Start-ServiceWindow {
    param(
        # Título que ajuda a identificar a janela durante a demonstração.
        [string]$WindowTitle,
        # Opções passadas ao programa Go para selecionar o serviço.
        [string]$Arguments
    )

    # O comando muda para a pasta do projeto e inicia o serviço no executável compilado.
    $command = "`$Host.UI.RawUI.WindowTitle = '$WindowTitle'; Set-Location -LiteralPath '$projectFolder'; & '$binaryPath' $Arguments"

    # -EncodedCommand evita problemas de aspas e espaços no caminho da pasta.
    $encodedCommand = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    Start-Process -FilePath "powershell.exe" -ArgumentList @("-NoExit", "-EncodedCommand", $encodedCommand)
}

# A origem é iniciada primeiro para que os caches possam consultá-la.
Start-ServiceWindow -WindowTitle "Mini CDN - Origem" -Arguments "-mode=origin -addr=:8080"
Start-Sleep -Milliseconds 500

# Cada cache é um processo separado e guarda sua própria cópia em memória.
Start-ServiceWindow -WindowTitle "Mini CDN - Edge A" -Arguments "-mode=edge -name=edge-a -addr=:8081 -origin=http://localhost:8080 -ttl=20s"
Start-Sleep -Milliseconds 250
Start-ServiceWindow -WindowTitle "Mini CDN - Edge B" -Arguments "-mode=edge -name=edge-b -addr=:8082 -origin=http://localhost:8080 -ttl=20s"
Start-Sleep -Milliseconds 250
Start-ServiceWindow -WindowTitle "Mini CDN - Edge C" -Arguments "-mode=edge -name=edge-c -addr=:8083 -origin=http://localhost:8080 -ttl=20s"
Start-Sleep -Milliseconds 750

# O gateway é iniciado por último para já encontrar os caches disponíveis.
Start-ServiceWindow -WindowTitle "Mini CDN - Gateway" -Arguments "-mode=gateway -addr=:8000 -origin=http://localhost:8080 -edges=http://localhost:8081,http://localhost:8082,http://localhost:8083 -health-interval=2s"

Write-Host "Serviços iniciados. Gateway: http://localhost:8000"
