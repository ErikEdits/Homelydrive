; Homelydrive Server – Windows installer (Inno Setup 6)
;   iscc /DAppVersion=1.2.3 packaging\windows\server.iss
; Expects dist\windows-amd64\homelydrive-server.exe (scripts/build.sh).

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif

[Setup]
AppId={{8C3B4F2E-6A71-4E0B-9C55-2F1D7E4A9B10}
AppName=Homelydrive Server
AppVersion={#AppVersion}
AppVerName=Homelydrive Server {#AppVersion}
AppPublisher=Homelydrive
AppPublisherURL=https://github.com/ErikEdits/Homelydrive
AppSupportURL=https://github.com/ErikEdits/Homelydrive/issues
AppUpdatesURL=https://github.com/ErikEdits/Homelydrive/releases
DefaultDirName={autopf}\Homelydrive Server
DefaultGroupName=Homelydrive
DisableProgramGroupPage=yes
LicenseFile=..\..\LICENSE
OutputDir=..\..\dist
OutputBaseFilename=Homelydrive-Server-Setup-{#AppVersion}
SetupIconFile=..\..\assets\logo.ico
UninstallDisplayIcon={app}\homelydrive-server.exe
UninstallDisplayName=Homelydrive Server
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
CloseApplications=no

[Languages]
Name: "de"; MessagesFile: "compiler:Languages\German.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
de.OpenWindow=Server-Fenster öffnen
en.OpenWindow=Open the server window
de.InstallService=Homelydrive-Dienst wird eingerichtet …
en.InstallService=Setting up the Homelydrive service …
de.TailscaleHint=Wichtig: Homelydrive ist nur über Tailscale erreichbar. Installiere Tailscale (tailscale.com) auf diesem PC und auf allen Geräten, die zugreifen sollen, und melde sie im selben Tailnet an.
en.TailscaleHint=Important: Homelydrive is only reachable through Tailscale. Install Tailscale (tailscale.com) on this PC and on every device that should connect, and sign them in to the same tailnet.

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "..\..\dist\windows-amd64\homelydrive-server.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Homelydrive Server"; Filename: "{app}\homelydrive-server.exe"; Parameters: "gui"
Name: "{autodesktop}\Homelydrive Server"; Filename: "{app}\homelydrive-server.exe"; Parameters: "gui"; Tasks: desktopicon

[Run]
; The service runs as LocalSystem; data lives in %ProgramData%\Homelydrive.
Filename: "{app}\homelydrive-server.exe"; Parameters: "service install"; StatusMsg: "{cm:InstallService}"; Flags: runhidden waituntilterminated
Filename: "{app}\homelydrive-server.exe"; Parameters: "service start"; StatusMsg: "{cm:InstallService}"; Flags: runhidden waituntilterminated
Filename: "{app}\homelydrive-server.exe"; Parameters: "gui"; Description: "{cm:OpenWindow}"; Flags: postinstall nowait skipifsilent runasoriginaluser

[UninstallRun]
Filename: "{app}\homelydrive-server.exe"; Parameters: "service stop"; Flags: runhidden waituntilterminated; RunOnceId: "StopService"
Filename: "{app}\homelydrive-server.exe"; Parameters: "service uninstall"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveService"

[Code]
// Stop a running service before its program file is replaced (updates).
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Exe: String;
  Code: Integer;
begin
  Result := '';
  Exe := ExpandConstant('{app}\homelydrive-server.exe');
  if FileExists(Exe) then
  begin
    Exec(Exe, 'service stop', '', SW_HIDE, ewWaitUntilTerminated, Code);
    Sleep(1500);
  end;
end;

procedure CurPageChanged(CurPageID: Integer);
begin
  if CurPageID = wpFinished then
    WizardForm.FinishedLabel.Caption := WizardForm.FinishedLabel.Caption + #13#10#13#10 + CustomMessage('TailscaleHint');
end;
