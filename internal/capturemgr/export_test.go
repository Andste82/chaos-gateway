package capturemgr

// SetKeylogTargetsChosen sets the hook that AppendKeylog calls between choosing the captures and writing to them; the
// function returned removes it.
func SetKeylogTargetsChosen(f func()) func() {
	keylogTargetsChosen = f
	return func() { keylogTargetsChosen = nil }
}
