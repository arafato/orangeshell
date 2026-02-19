package config

// --- Env Variables messages ---
// These message types are shared between the config tab's env vars category
// and the app-level command functions that write to wrangler config files.

// SetVarMsg requests the app to write a var into the wrangler config.
type SetVarMsg struct {
	ConfigPath string
	EnvName    string
	VarName    string
	Value      string
}

// DeleteVarMsg requests the app to remove a var from the wrangler config.
type DeleteVarMsg struct {
	ConfigPath string
	EnvName    string
	VarName    string
}

// SetVarDoneMsg delivers the result of a SetVar operation.
type SetVarDoneMsg struct {
	Err error
}

// DeleteVarDoneMsg delivers the result of a DeleteVar operation.
type DeleteVarDoneMsg struct {
	Err error
}

// --- Triggers messages ---
// These message types are shared between the config tab's triggers category
// and the app-level command functions that write to wrangler config files.

// AddCronMsg requests the app to add a cron trigger to the wrangler config.
type AddCronMsg struct {
	ConfigPath string
	Cron       string
}

// DeleteCronMsg requests the app to remove a cron trigger from the wrangler config.
type DeleteCronMsg struct {
	ConfigPath string
	Cron       string
}

// AddCronDoneMsg delivers the result of an AddCron operation.
type AddCronDoneMsg struct {
	Err error
}

// DeleteCronDoneMsg delivers the result of a DeleteCron operation.
type DeleteCronDoneMsg struct {
	Err error
}
