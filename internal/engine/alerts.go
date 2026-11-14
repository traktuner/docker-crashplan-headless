package engine

// Alert semantics of the stock 12.1.1 desktop app:
//   - shared/constants/alert_constants.js (names, priority, text keys)
//   - shared/models/alert_model.js (title/message only from the locale for
//     non-custom alerts, canClose)
//   - shared/components/alert.js (Resume / Retry links)
//   - shared/utils/local_alert_generator.js (version alerts from the cloud's DCODE)
// English texts are the "en" locale strings of the 12.1.1 app bundle.

type alertSpec struct {
	priority int
	title    string
	message  string
	closable bool
	action   string
}

// otherAlertPriority sorts engine alerts the stock app has no entry for after
// all known ones.
const otherAlertPriority = 9

var stockAlerts = map[string]alertSpec{
	"UpgradeRequiredErrorAlert": {priority: 1, title: "Upgrade required:", message: "You have an older version of CrashPlan."},
	"VersionMismatchErrorAlert": {priority: 1, title: "Version mismatch:", message: "You are running a different version of CrashPlan than the server. Contact your administrator to resolve this problem."},
	"DownloadingPatchAlert":     {priority: 2, closable: true, title: "Upgrade Available:", message: "Downloading a version of CrashPlan now."},
	"ApplyingPatchAlert":        {priority: 3, closable: true, message: "Upgrading to a new version of CrashPlan now. CrashPlan will be temporarily unavailable during the upgrade."},
	"PatchFailedAlert":          {priority: 4, closable: true, action: "retry", message: "CrashPlan wasn't able to upgrade but will reattempt in one hour."},
	"DuplicateIdentityAlert":    {priority: 5, closable: true, title: "Duplicate Computers Detected:", message: "Backup stopped because another device is using the same Device ID. Uninstall CrashPlan from one of the devices."},
	"RealtimeWatcherAlert":      {priority: 6, closable: true},
	"CustomAlert":               {priority: 7, closable: true},
	// The stock message carries "in ${remainingTime}"; the engine alert has no
	// time, so the sentence ends without it.
	"EnginePausedAlert": {priority: 8, action: "resume", title: "Paused:", message: "CrashPlan will automatically resume."},
}

// onboardingAlerts are engine help/welcome alerts with empty texts that the
// stock app never shows.
var onboardingAlerts = map[string]bool{"HelpIntroAlert": true, "HelpWelcomeAlert": true, "HepPersonalizeAlert": true}

// presentAlert adds stock priority, closability, action and texts. Custom
// alerts keep the engine's title and message, as in the stock app.
func presentAlert(a Alert) Alert {
	spec, known := stockAlerts[a.Name]
	if !known {
		a.Priority, a.Closable = otherAlertPriority, true
		return a
	}
	a.Priority, a.Closable, a.Action = spec.priority, spec.closable, spec.action
	if a.Name != "CustomAlert" {
		if a.Title == "" {
			a.Title = spec.title
		}
		if a.Message == "" {
			a.Message = spec.message
		}
	}
	return a
}

// localVersionAlert is local_alert_generator.js: the cloud destination's
// disconnect code NEW_VERSION or OLD_VERSION raises a version alert unless an
// upgrade is being downloaded or applied.
func localVersionAlert(devices []Device, engine []Alert) (Alert, bool) {
	for _, a := range engine {
		if a.Name == "DownloadingPatchAlert" || a.Name == "ApplyingPatchAlert" {
			return Alert{}, false
		}
	}
	code := ""
	for _, d := range devices {
		if d.Cloud {
			code = d.DisconnectCode
			break
		}
	}
	name := ""
	switch code {
	case "NEW_VERSION":
		name = "VersionMismatchErrorAlert"
	case "OLD_VERSION":
		name = "UpgradeRequiredErrorAlert"
	default:
		return Alert{}, false
	}
	for _, a := range engine {
		if a.Name == name {
			return Alert{}, false
		}
	}
	return presentAlert(Alert{ID: name, Name: name, Expiration: "-1"}), true
}
