# Submit the MSIX a release built to the Microsoft Store and send it to
# certification.
#
#   pwsh msstore-submit.ps1 -Msix <path to .msix> -Version 0.47.0
#
# Needs MSSTORE_TENANT_ID, MSSTORE_SELLER_ID, MSSTORE_CLIENT_ID,
# MSSTORE_CLIENT_SECRET and MSSTORE_PRODUCT_ID in the environment, and the
# msstore CLI on the PATH (microsoft/microsoft-store-apppublisher).
#
# The credentials are an Entra ID app ("filex release pipeline") with the
# Manager role in Partner Center, in the brftech.onmicrosoft.com tenant
# associated with the account (2026-09-26).
#
# 1. A submission still on its way (in certification, being published) cannot
#    be replaced: the release says so and leaves the Store alone. Upload the
#    msix-<tag> artifact in Partner Center once that one is live.
# 2. A pending submission that is going nowhere (a draft, or one that failed)
#    is deleted: this release supersedes it.
# 3. msstore uploads the package into a new draft, the listing's "What's new"
#    points at the GitHub Release in every listed language, and the draft is
#    committed, which sends it to certification.
#
# Nothing here fails the release: a Store problem is a warning on the run, and
# the msix-<tag> artifact is kept either way.
param(
  [Parameter(Mandatory)][string]$Msix,
  [Parameter(Mandatory)][string]$Version
)
$ErrorActionPreference = 'Stop'

function Skip([string]$msg) {
  Write-Host "::warning title=Microsoft Store::$msg"
  exit 0
}

try {
  $app = $env:MSSTORE_PRODUCT_ID
  $api = "https://manage.devcenter.microsoft.com/v1.0/my/applications/$app"
  $tok = Invoke-RestMethod -Method Post "https://login.microsoftonline.com/$env:MSSTORE_TENANT_ID/oauth2/token" -Body @{
    grant_type    = 'client_credentials'
    client_id     = $env:MSSTORE_CLIENT_ID
    client_secret = $env:MSSTORE_CLIENT_SECRET
    resource      = 'https://manage.devcenter.microsoft.com'
  }
  $h = @{ Authorization = "Bearer $($tok.access_token)" }

  $pending = (Invoke-RestMethod -Headers $h $api).pendingApplicationSubmission
  if ($pending) {
    $state = (Invoke-RestMethod -Headers $h "$api/submissions/$($pending.id)/status").status
    $onItsWay = 'CommitStarted', 'PreProcessing', 'Certification', 'Release', 'PendingPublication', 'Publishing'
    if ($onItsWay -contains $state) {
      Skip "submission $($pending.id) is still $state, so $Version was not submitted. Upload the msix-v$Version artifact in Partner Center once that one is live."
    }
    Write-Host "Deleting pending submission $($pending.id) ($state): $Version replaces it."
    Invoke-RestMethod -Method Delete -Headers $h "$api/submissions/$($pending.id)" | Out-Null
  }

  msstore reconfigure --tenantId $env:MSSTORE_TENANT_ID --sellerId $env:MSSTORE_SELLER_ID --clientId $env:MSSTORE_CLIENT_ID --clientSecret $env:MSSTORE_CLIENT_SECRET
  if ($LASTEXITCODE -ne 0) { Skip "msstore reconfigure failed (exit $LASTEXITCODE)." }
  msstore publish desktop -i $Msix -id $app --noCommit
  if ($LASTEXITCODE -ne 0) { Skip "msstore could not upload $Msix (exit $LASTEXITCODE); see the log above." }

  $sid = (Invoke-RestMethod -Headers $h $api).pendingApplicationSubmission.id
  if (-not $sid) { Skip "msstore reported success but no draft submission exists." }
  $sub = Invoke-RestMethod -Headers $h "$api/submissions/$sid"
  $url = "https://github.com/BRF-Tech/filex/releases/tag/v$Version"
  try {
    foreach ($lang in $sub.listings.PSObject.Properties.Name) {
      $notes = if ($lang -like 'tr*') { "filex $Version sürümündeki yenilikler: $url" } else { "What's new in filex ${Version}: $url" }
      $sub.listings.$lang.baseListing | Add-Member -NotePropertyName releaseNotes -NotePropertyValue $notes -Force
    }
    $body = [Text.Encoding]::UTF8.GetBytes(($sub | ConvertTo-Json -Depth 32))
    Invoke-RestMethod -Method Put -Headers $h -ContentType 'application/json; charset=utf-8' -Body $body "$api/submissions/$sid" | Out-Null
  } catch {
    Write-Host "::warning title=Microsoft Store::'What's new' was not updated ($($_.Exception.Message)); submitting with the previous text."
  }

  Invoke-RestMethod -Method Post -Headers $h "$api/submissions/$sid/commit" | Out-Null
  $state = 'CommitStarted'
  for ($i = 0; $i -lt 12 -and $state -eq 'CommitStarted'; $i++) {
    Start-Sleep -Seconds 10
    $state = (Invoke-RestMethod -Headers $h "$api/submissions/$sid/status").status
  }
  if ($state -like '*Failed') { Skip "submission $sid for $Version is $state; see Partner Center." }
  Write-Host "Microsoft Store: $Version submitted as $sid, now $state."
} catch {
  Skip "the submission did not go through: $($_.Exception.Message)"
}
exit 0
