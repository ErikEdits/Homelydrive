; Homelydrive PC app – Windows installer (Inno Setup 6)
;   iscc /DAppVersion=1.2.3 packaging\windows\client.iss
; Expects dist\windows-amd64\homelydrive.exe (scripts/build.sh) and
; dist\winfsp.msi (WinFsp, needed for the virtual drive).

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif

[Setup]
AppId={{3E9A1C47-52D8-4B6F-A0E3-7C2B91D46F58}
AppName=Homelydrive
AppVersion={#AppVersion}
AppVerName=Homelydrive {#AppVersion}
AppPublisher=Homelydrive
AppPublisherURL=https://github.com/ErikEdits/Homelydrive
AppSupportURL=https://github.com/ErikEdits/Homelydrive/issues
AppUpdatesURL=https://github.com/ErikEdits/Homelydrive/releases
DefaultDirName={autopf}\Homelydrive
DefaultGroupName=Homelydrive
DisableProgramGroupPage=yes
LicenseFile=..\..\LICENSE
OutputDir=..\..\dist
OutputBaseFilename=Homelydrive-Setup-{#AppVersion}
SetupIconFile=..\..\assets\logo.ico
UninstallDisplayIcon={app}\homelydrive.exe
UninstallDisplayName=Homelydrive
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
; WinFsp (virtual drive) needs administrator rights.
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
CloseApplications=no

[Languages]
Name: "de"; MessagesFile: "compiler:Languages\German.isl"
Name: "en"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
de.InstallWinFsp=WinFsp (für das virtuelle Laufwerk) wird installiert …
en.InstallWinFsp=Installing WinFsp (for the virtual drive) …

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "..\..\dist\windows-amd64\homelydrive.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\winfsp.msi"; DestDir: "{tmp}"; Flags: deleteafterinstall; Check: not WinFspInstalled

[Icons]
Name: "{group}\Homelydrive"; Filename: "{app}\homelydrive.exe"
Name: "{autodesktop}\Homelydrive"; Filename: "{app}\homelydrive.exe"; Tasks: desktopicon

[Run]
Filename: "msiexec.exe"; Parameters: "/i ""{tmp}\winfsp.msi"" /qn /norestart"; StatusMsg: "{cm:InstallWinFsp}"; Flags: runhidden waituntilterminated; Check: not WinFspInstalled
; Right-click menu entries for the user who runs the setup.
Filename: "{app}\homelydrive.exe"; Parameters: "integration install"; Flags: runhidden waituntilterminated runasoriginaluser
Filename: "{app}\homelydrive.exe"; Description: "{cm:LaunchProgram,Homelydrive}"; Flags: postinstall nowait skipifsilent runasoriginaluser

[UninstallRun]
Filename: "{app}\homelydrive.exe"; Parameters: "quit"; Flags: runhidden waituntilterminated; RunOnceId: "QuitApp"
Filename: "{app}\homelydrive.exe"; Parameters: "integration uninstall"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveMenu"

[Code]
function WinFspInstalled: Boolean;
begin
  Result := RegKeyExists(HKLM32, 'SOFTWARE\WinFsp') or RegKeyExists(HKLM64, 'SOFTWARE\WinFsp');
end;

// Close a running app before its program file is replaced (updates).
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Exe: String;
  Code: Integer;
begin
  Result := '';
  Exe := ExpandConstant('{app}\homelydrive.exe');
  if FileExists(Exe) then
  begin
    ExecAsOriginalUser(Exe, 'quit', '', SW_HIDE, ewWaitUntilTerminated, Code);
    Sleep(1500);
  end;
end;
