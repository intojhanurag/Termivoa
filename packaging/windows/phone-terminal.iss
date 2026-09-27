; Per-user Inno Setup installer for Termivoa. Build it with
; packaging/windows/build-installer.ps1, which defines the values below.
; No administrator rights, services, or machine-wide changes: files go under
; %LOCALAPPDATA%\Programs\Phone Terminal and startup uses two per-user
; scheduled tasks registered by `pterm setup --apply --yes`.

#ifndef AppVersion
  #error Define AppVersion, NumericVersion and SourceExe (use build-installer.ps1)
#endif
#ifndef OutputDir
  #define OutputDir "..\..\artifacts"
#endif
#ifdef Sign
  #define Label ""
#else
  #define Label "-unsigned"
#endif
; Each version is staged in its own directory so an update never overwrites
; the executable a running broker uses; the previous version stays for rollback.
#define ExeDir "{app}\versions\" + AppVersion

[Setup]
AppId={{EBF51E47-4F36-40E1-9A74-D41FF979ED6D}
AppName=Termivoa
AppVersion={#AppVersion}
AppVerName=Termivoa {#AppVersion}
AppPublisher=Termivoa contributors
VersionInfoVersion={#NumericVersion}
; The install and data directory names stay "Phone Terminal" so existing installs,
; startup tasks and data keep working (docs/naming.md).
DefaultDirName={localappdata}\Programs\Phone Terminal
DefaultGroupName=Termivoa
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
; conhost --headless, used by the startup tasks, needs Windows 10 1809 or later.
MinVersion=10.0.17763
OutputDir={#OutputDir}
OutputBaseFilename=Termivoa-{#AppVersion}-windows-x64{#Label}-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayName=Termivoa
UninstallDisplayIcon={#ExeDir}\pterm.exe
; Never close a running broker: that would end its live sessions.
CloseApplications=no
RestartApplications=no
ChangesEnvironment=yes
SetupLogging=yes
#ifdef Sign
SignTool=pterm
SignedUninstaller=yes
#endif

[Tasks]
Name: "startup"; Description: "Start Termivoa when I sign in (two scheduled tasks for my account only: broker and gateway)"
Name: "path"; Description: "Add pterm to my PATH (my account only)"; Flags: unchecked

[InstallDelete]
; Shortcuts from before the rename to Termivoa.
Type: files; Name: "{group}\Phone Terminal.lnk"
Type: files; Name: "{group}\Uninstall Phone Terminal.lnk"

[Files]
Source: "{#SourceExe}"; DestDir: "{#ExeDir}"; DestName: "pterm.exe"; Flags: ignoreversion

[Icons]
Name: "{group}\Termivoa"; Filename: "{#ExeDir}\pterm.exe"; Parameters: "launch"; Comment: "Start Termivoa and open its local page"; Flags: runminimized
Name: "{group}\Uninstall Termivoa"; Filename: "{uninstallexe}"

[Run]
Filename: "{#ExeDir}\pterm.exe"; Parameters: "launch"; Description: "Open Termivoa now"; Flags: postinstall nowait skipifsilent runhidden

[Code]
function RunPterm(Params: String; var Code: Integer): Boolean;
begin
  Result := Exec(ExpandConstant('{#ExeDir}\pterm.exe'), Params, '', SW_HIDE, ewWaitUntilTerminated, Code);
end;

{ Rewrite the user's PATH without any Termivoa version directory, then
  append Addition if it is not empty. Other entries keep their order. }
procedure UpdateUserPath(Addition: String);
var
  Original, Rest, Entry, Updated, Prefix: String;
  Position: Integer;
begin
  if not RegQueryStringValue(HKCU, 'Environment', 'Path', Original) then
    Original := '';
  Prefix := Lowercase(ExpandConstant('{app}\versions\'));
  Rest := Original;
  Updated := '';
  while Rest <> '' do
  begin
    Position := Pos(';', Rest);
    if Position = 0 then
    begin
      Entry := Rest;
      Rest := '';
    end
    else
    begin
      Entry := Copy(Rest, 1, Position - 1);
      Delete(Rest, 1, Position);
    end;
    if (Entry <> '') and (Pos(Prefix, Lowercase(Entry)) <> 1) then
    begin
      if Updated <> '' then
        Updated := Updated + ';';
      Updated := Updated + Entry;
    end;
  end;
  if Addition <> '' then
  begin
    if Updated <> '' then
      Updated := Updated + ';';
    Updated := Updated + Addition;
  end;
  if Updated <> Original then
    RegWriteExpandStringValue(HKCU, 'Environment', 'Path', Updated);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Code: Integer;
begin
  if CurStep <> ssPostInstall then
    Exit;
  if WizardIsTaskSelected('path') then
    UpdateUserPath(ExpandConstant('{#ExeDir}'))
  else
    UpdateUserPath('');
  if WizardIsTaskSelected('startup') then
  begin
    if not RunPterm('setup --apply --yes', Code) or (Code <> 0) then
      SuppressibleMsgBox('Termivoa is installed, but startup registration did not complete (exit code ' + IntToStr(Code) + '). ' +
        'The broker may have active sessions, or Termivoa may already be running without startup registration. ' +
        'Run "pterm setup --apply" to see why and what to stop first.', mbError, MB_OK, IDOK);
  end
  else if not RunPterm('uninstall --yes', Code) or (Code <> 0) then
    SuppressibleMsgBox('Existing Termivoa startup tasks could not be removed (exit code ' + IntToStr(Code) + '). ' +
      'Run "pterm uninstall" to see why.', mbError, MB_OK, IDOK);
end;

var
  UninstallParams: String;

{ Ask about data first, then check with exactly those options: pterm exits
  non-zero while the broker has active sessions, does not answer, or (with
  data deletion) runs outside startup registration, and nothing is removed.
  A missing pterm.exe does not block removal. }
function InitializeUninstall(): Boolean;
var
  Code: Integer;
begin
  Result := True;
  UninstallParams := 'uninstall --yes';
  if not UninstallSilent() and (MsgBox('Also delete your Termivoa data (paired devices, projects, settings and logs)?' + #13#10#13#10 +
    'Choose No to keep it for a later reinstall.', mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES) then
    UninstallParams := UninstallParams + ' --delete-data';
  if RunPterm(UninstallParams + ' --dry-run', Code) and (Code <> 0) then
  begin
    SuppressibleMsgBox('Termivoa cannot be uninstalled now: its broker has active sessions, did not answer, or is running without startup registration. ' +
      'Run "pterm uninstall" to see why, finish or terminate the sessions (pterm list, pterm terminate), stop the broker it names, and try again.', mbError, MB_OK, IDOK);
    Result := False;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Code: Integer;
begin
  if CurUninstallStep = usUninstall then
  begin
    if not RunPterm(UninstallParams, Code) or (Code <> 0) then
      SuppressibleMsgBox('Termivoa uninstall did not complete (exit code ' + IntToStr(Code) + '). ' +
        'Your data is kept if a broker was still running. Run "pterm uninstall" to see why; remove any leftover ' +
        '"Phone Terminal Broker" and "Phone Terminal Gateway" tasks in Task Scheduler.', mbError, MB_OK, IDOK);
  end;
  if CurUninstallStep = usPostUninstall then
    UpdateUserPath('');
end;
