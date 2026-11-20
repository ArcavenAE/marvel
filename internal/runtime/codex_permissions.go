package runtime

// PermissionMapper is implemented by an adapter that turns a role's
// permissions into its harness's own flags. refusal is non-empty when the
// harness has no ruled mapping for the mode, and args is then nil.
type PermissionMapper interface {
	MapPermissions(mode string, dangerous, headless bool) (args []string, refusal string)
}

// MapPermissions is a stub until the mapping is written.
func (c *Codex) MapPermissions(_ string, _, _ bool) ([]string, string) {
	return nil, ""
}
