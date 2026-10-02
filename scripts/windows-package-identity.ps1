function Assert-WindowsPackageIdentity([string]$PackagePath, [string]$ExpectedId) {
    $archive = [System.IO.Compression.ZipFile]::OpenRead($PackagePath)
    try {
        $manifests = @($archive.Entries | Where-Object { $_.FullName -match '^[^/\\]+\.nuspec$' })
        if ($manifests.Count -ne 1) {
            throw "Expected one root package manifest in $PackagePath; found $($manifests.Count)."
        }
        $stream = $manifests[0].Open()
        try {
            $settings = [System.Xml.XmlReaderSettings]::new()
            $settings.DtdProcessing = [System.Xml.DtdProcessing]::Prohibit
            $settings.XmlResolver = $null
            $reader = [System.Xml.XmlReader]::Create($stream, $settings)
            try {
                $document = [System.Xml.XmlDocument]::new()
                $document.XmlResolver = $null
                $document.Load($reader)
                $id = $document.SelectSingleNode("/*[local-name()='package']/*[local-name()='metadata']/*[local-name()='id']")
                if ($null -eq $id -or $id.InnerText -cne $ExpectedId) {
                    throw "Package identity mismatch: expected $ExpectedId in $PackagePath."
                }
            }
            finally {
                $reader.Dispose()
            }
        }
        finally {
            $stream.Dispose()
        }
    }
    finally {
        $archive.Dispose()
    }
}
