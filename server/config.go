// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/ini.v1"
)

const (
	DbTypePostgresql string = "postgresql"
)

type Config struct {
	BaseDir              string
	ConfigFile           string
	DbType               string
	DbHost               string
	DbPort               uint
	DbName               string
	DbUsername           string
	DbPassword           string
	Listen               string
	SslAutoCert          string
	SslCaCertFile        string
	SslCaKeyFile         string
	SslCertFile          string
	SslKeyFile           string
	SslListen            string
	EnableDebugLog       bool
	UseOpus              bool   // Enable Opus encoding for new calls
	OpusMigration        bool   // Migrate existing calls to Opus on startup
	daemon               *Daemon
	newAdminPassword     string
	migrateToOpus        bool
	migrateOpusBatch     int
	migrateOpusDryRun    bool
}

func NewConfig() *Config {
	const (
		defaultAdminUrl         = "/admin"
		defaultConfigFile       = "thinline-radio.ini"
		defaultDbType           = DbTypePostgresql
		defaultDbHost           = "localhost"
		defaultDbPortPostgreSql = uint(5432)
		defaultListen           = ":3000"
	)

	var (
		command       = flag.String(COMMAND_ARG, "", fmt.Sprintf("advanced administrative tasks (use -%s %s for usage)", COMMAND_ARG, COMMAND_HELP))
		config        = &Config{}
		configSave    = flag.Bool("config_save", false, fmt.Sprintf("save configuration to %s", defaultConfigFile))
		serviceAction = flag.String("service", "", "service command, one of start, stop, restart, install, uninstall")
		version       = flag.Bool("version", false, "show application version")
		migrateOpus   = flag.Bool("migrate_to_opus", false, "convert all M4A/AAC audio to Opus format (50% storage savings)")
		migrateBatch  = flag.Int("migrate_batch_size", 100, "number of calls to process per batch during migration")
		migrateDryRun = flag.Bool("migrate_dry_run", false, "preview migration without making changes")
	)

	if exe, err := os.Executable(); err == nil {
		if !regexp.MustCompile(`go-build[0-9]+`).Match([]byte(exe)) {
			config.BaseDir = filepath.Dir(exe)
			if !config.isBaseDirWritable() {
				if h, err := os.UserHomeDir(); err == nil {
					config.BaseDir = filepath.Join(h, "ThinLine Radio")
					if _, err := os.Stat(config.BaseDir); os.IsNotExist(err) {
						os.MkdirAll(config.BaseDir, 0770)
					}
				}
			}
		}
	}

	flag.StringVar(&config.BaseDir, "base_dir", config.BaseDir, "base directory where all data will be written")
	flag.StringVar(&config.DbHost, "db_host", defaultDbHost, "database host ip or hostname")
	flag.StringVar(&config.DbName, "db_name", "", "database name")
	flag.StringVar(&config.DbPassword, "db_pass", "", "database password")
	flag.UintVar(&config.DbPort, "db_port", defaultDbPortPostgreSql, "database host port")
	flag.StringVar(&config.DbType, "db_type", defaultDbType, "database type (postgresql)")
	flag.StringVar(&config.DbUsername, "db_user", "", "database user name")
	flag.StringVar(&config.ConfigFile, "config", defaultConfigFile, "server config file")
	flag.StringVar(&config.Listen, "listen", defaultListen, "listening address")
	flag.StringVar(&config.newAdminPassword, "admin_password", "", "change admin password")
	flag.StringVar(&config.SslAutoCert, "ssl_auto_cert", "", "domain name for Let's Encrypt automatic certificate")
	flag.StringVar(&config.SslCertFile, "ssl_cert_file", "", "ssl PEM formated certificate")
	flag.StringVar(&config.SslKeyFile, "ssl_key_file", "", "ssl PEM formated key")
	flag.StringVar(&config.SslListen, "ssl_listen", "", "listening address for ssl")
	flag.Parse()

	if !config.isBaseDirWritable() {
		log.Fatalf("no write permissions in %s", config.BaseDir)
	}

	switch {
	case *configSave:
		if err := config.saveConfig(); err == nil {
			fmt.Printf("%s file created\n", config.ConfigFile)
			os.Exit(0)
		} else {
			fmt.Printf("error: %s\n", err.Error())
			os.Exit(-1)
		}

	case *version:
		fmt.Println(Version)
		os.Exit(0)

	default:
		if cfg, err := ini.Load(config.GetConfigFilePath()); err == nil {
			if v := cfg.Section("").Key("db_host").String(); len(v) > 0 {
				config.DbHost = v
			}

			if v := cfg.Section("").Key("db_name").String(); len(v) > 0 {
				config.DbName = v
			}

			if v := cfg.Section("").Key("db_pass").String(); len(v) > 0 {
				config.DbPassword = v
			}

			if v := cfg.Section("").Key("db_type").String(); len(v) > 0 {
				config.DbType = v
			}

			if config.DbPort, err = cfg.Section("").Key("db_port").Uint(); err != nil {
				config.DbPort = defaultDbPortPostgreSql
			}

			if v := cfg.Section("").Key("db_user").String(); len(v) > 0 {
				config.DbUsername = v
			}

			if v := cfg.Section("").Key("listen").String(); len(v) > 0 {
				config.Listen = v
			}

			if v := cfg.Section("").Key("ssl_auto_cert").String(); len(v) > 0 {
				config.SslAutoCert = v
			}

			if v := cfg.Section("").Key("ssl_cert_file").String(); len(v) > 0 {
				config.SslCertFile = v
			}

			if v := cfg.Section("").Key("ssl_key_file").String(); len(v) > 0 {
				config.SslKeyFile = v
			}

			if v := cfg.Section("").Key("ssl_listen").String(); len(v) > 0 {
				config.SslListen = v
			}

		// Read enable_debug_log option (defaults to false)
		if v, err := cfg.Section("").Key("enable_debug_log").Bool(); err == nil {
			config.EnableDebugLog = v
		}
		
		// Read opus settings (defaults to false for backward compatibility)
		if v, err := cfg.Section("").Key("opus").Bool(); err == nil {
			config.UseOpus = v
		}
		
		// Read opus_migration setting (defaults to false)
		if v, err := cfg.Section("").Key("opus_migration").Bool(); err == nil {
			config.OpusMigration = v
		}
	}

		if config.DbType != DbTypePostgresql {
			fmt.Printf("unknown database type %s (only postgresql is supported)\n", config.DbType)
			return nil
		}
	}

	if *command != "" {
		NewCommand(config.BaseDir).Do(*command)
	}
	
	config.migrateToOpus = *migrateOpus
	config.migrateOpusBatch = *migrateBatch
	config.migrateOpusDryRun = *migrateDryRun

	if *serviceAction != "" {
		daemon, err := NewDaemon()
		if err != nil {
			log.Printf("ERROR: Failed to initialize daemon service: %v", err)
			log.Printf("Daemon operations are not available. Exiting.")
			os.Exit(1)
		}
		config.daemon = daemon.Control(*serviceAction)
	}

	return config
}

func (config *Config) GetConfigFilePath() string {
	return config.GetPath(config.ConfigFile)
}

func (config *Config) GetPath(p string) string {
	if path.IsAbs(p) {
		return p
	}
	return filepath.Join(config.BaseDir, p)
}

// SetOpusMigration updates the opus_migration setting in the INI file
func (config *Config) SetOpusMigration(enabled bool) error {
	configPath := config.GetConfigFilePath()
	
	// Load the INI file
	cfg, err := ini.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config file: %v", err)
	}
	
	// Set the opus_migration value
	cfg.Section("").Key("opus_migration").SetValue(strconv.FormatBool(enabled))
	
	// Save the INI file
	if err := cfg.SaveTo(configPath); err != nil {
		return fmt.Errorf("failed to save config file: %v", err)
	}
	
	return nil
}

func (config *Config) GetSslCaCertFilePath() string {
	return config.GetPath(config.SslCaCertFile)
}

func (config *Config) GetSslCaKeyFilePath() string {
	return config.GetPath(config.SslCaKeyFile)
}

func (config *Config) GetSslCertFilePath() string {
	return config.GetPath(config.SslCertFile)
}

func (config *Config) GetSslKeyFilePath() string {
	return config.GetPath(config.SslKeyFile)
}

func (config *Config) isBaseDirWritable() bool {
	if f, err := os.CreateTemp(config.BaseDir, ".tmp*"); err == nil {
		f.Close()
		os.Remove(f.Name())
		return true
	}
	return false
}

func (config *Config) saveConfig() error {
	ini := []string{}

	if config.DbHost != "" {
		ini = append(ini, fmt.Sprintf("db_host = %s", config.DbHost))
	}

	if config.DbName != "" {
		ini = append(ini, fmt.Sprintf("db_name = %s", config.DbName))
	}

	if config.DbPassword != "" {
		ini = append(ini, fmt.Sprintf("db_pass = %s", config.DbPassword))
	}

	if config.DbPort > 0 {
		ini = append(ini, fmt.Sprintf("db_port = %s", strconv.Itoa(int(config.DbPort))))
	}

	if config.DbType != "" {
		ini = append(ini, fmt.Sprintf("db_type = %s", config.DbType))
	}

	if config.DbUsername != "" {
		ini = append(ini, fmt.Sprintf("db_user = %s", config.DbUsername))
	}

	if config.Listen != "" {
		ini = append(ini, fmt.Sprintf("listen = %s", config.Listen))
	}

	if config.SslAutoCert != "" {
		ini = append(ini, fmt.Sprintf("ssl_auto_cert = %s", config.SslAutoCert))
	}

	if config.SslCertFile != "" {
		ini = append(ini, fmt.Sprintf("ssl_cert_file = %s", config.SslCertFile))
	}

	if config.SslKeyFile != "" {
		ini = append(ini, fmt.Sprintf("ssl_key_file = %s", config.SslKeyFile))
	}

	if config.SslListen != "" {
		ini = append(ini, fmt.Sprintf("ssl_listen = %s", config.SslListen))
	}

	if config.EnableDebugLog {
		ini = append(ini, "enable_debug_log = true")
	}

	file, err := os.Create(config.GetConfigFilePath())
	if err != nil {
		return err
	}

	for _, line := range ini {
		_, err := file.WriteString(line + "\n")
		if err != nil {
			return err
		}
	}

	return file.Close()
}
