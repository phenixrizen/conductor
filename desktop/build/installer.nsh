; Conductor's NSIS additions (electron-builder nsis.include).
; The inbound rule for the UDP port every WebRTC connection uses (the app's
; forwarder on Windows, in front of the server in WSL). netsh needs
; administrator rights: a per-user install without them leaves the rule to
; the app, which offers it from Settings (an elevated netsh, once).
!macro customInstall
  nsExec::ExecToLog 'netsh advfirewall firewall add rule name="Conductor WebRTC (UDP 7877)" dir=in action=allow protocol=UDP localport=7877'
  Pop $0
!macroend

!macro customUnInstall
  nsExec::ExecToLog 'netsh advfirewall firewall delete rule name="Conductor WebRTC (UDP 7877)"'
  Pop $0
!macroend
