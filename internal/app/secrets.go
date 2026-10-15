package app

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// MoveSecrets moves the values of password preferences found in cfg (from an
// older version, or an imported file) into the secrets store and removes
// them from cfg. A value that cannot be stored stays in cfg, and the errors
// are returned joined.
func (e *Env) MoveSecrets(cfg *config.Config) error {
	_, err := moveSecrets(cfg, e.manifests, e.Services.Secrets, e.Services.Log)
	return err
}

// migrateSecrets moves password preferences out of the configuration file at
// startup (design §6: they are never written to config.json).
func (e *Env) migrateSecrets(secrets host.Secrets, log *slog.Logger) {
	cfg := e.Config.Get()
	moved, err := moveSecrets(&cfg, e.manifests, secrets, log)
	if err != nil {
		log.Error("moving password preferences to the Credential Manager", "err", err)
	}
	if moved == 0 {
		return
	}
	if err := e.Config.Update(func(c *config.Config) { *c = cfg }); err != nil {
		log.Error("removing password preferences from the configuration", "err", err)
	}
}

func moveSecrets(cfg *config.Config, manifests []plugin.Manifest, secrets host.Secrets, log *slog.Logger) (moved int, err error) {
	var errs []error
	for _, m := range manifests {
		pc, ok := cfg.Plugins[m.ID]
		if !ok || len(pc.Preferences) == 0 {
			continue
		}
		for _, pref := range m.Preferences {
			raw, ok := pc.Preferences[pref.Name]
			if pref.Type != plugin.PrefPassword || !ok {
				continue
			}
			var secret string
			if err := json.Unmarshal(raw, &secret); err != nil {
				secret = string(raw)
			}
			if secret != "" {
				if err := secrets.Set(m.ID, pref.Name, secret); err != nil {
					errs = append(errs, fmt.Errorf("%s/%s: %w", m.ID, pref.Name, err))
					continue
				}
			}
			delete(pc.Preferences, pref.Name)
			moved++
			log.Info("password preference moved out of the configuration", "plugin", m.ID, "pref", pref.Name)
		}
		cfg.Plugins[m.ID] = pc
	}
	return moved, errors.Join(errs...)
}
