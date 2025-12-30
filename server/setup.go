// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
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
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	_ "github.com/lib/pq"
	"golang.org/x/term"
)

// checkPostgreSQLInstalled checks if PostgreSQL is installed and accessible
func checkPostgreSQLInstalled() bool {
	// Check for psql command
	cmd := exec.Command("psql", "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// readPassword reads a password from stdin without echoing
func readPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	bytePassword, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(bytePassword), nil
}

// readInput reads a line from stdin
func readInput(prompt string, defaultValue string) string {
	reader := bufio.NewReader(os.Stdin)
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultValue)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" && defaultValue != "" {
		return defaultValue
	}
	return input
}

// runInteractiveSetup runs the interactive setup wizard
func runInteractiveSetup(configFile string) error {
	fmt.Println()
	fmt.Println("                │                    ████████╗██╗  ██╗██╗███╗   ██╗██╗     ██╗███╗   ██╗███████╗")
	fmt.Println("                │                    ╚══██╔══╝██║  ██║██║████╗  ██║██║     ██║████╗  ██║██╔════╝")
	fmt.Println("                │                       ██║   ███████║██║██╔██╗ ██║██║     ██║██╔██╗ ██║█████╗  ")
	fmt.Println("                ▲                       ██║   ██╔══██║██║██║╚██╗██║██║     ██║██║╚██╗██║██╔══╝  ")
	fmt.Println("      ╔═════════════════════╗           ██║   ██║  ██║██║██║ ╚████║███████╗██║██║ ╚████║███████╗")
	fmt.Println("      ║   OHIO MARCS-IP     ║           ╚═╝   ╚═╝  ╚═╝╚═╝╚═╝  ╚═══╝╚══════╝╚═╝╚═╝  ╚═══╝╚══════╝")
	fmt.Println("      ║   78 FD DISPATCH    ║                        ██████╗  █████╗ ██████╗ ██╗ ██████╗       ")
	fmt.Println("      ║   TGID: 46036       ║                        ██╔══██╗██╔══██╗██╔══██╗██║██╔═══██╗      ")
	fmt.Println("      ║                     ║                        ██████╔╝███████║██║  ██║██║██║   ██║      ")
	fmt.Println("      ╠═════════════════════╣                        ██╔══██╗██╔══██║██║  ██║██║██║   ██║      ")
	fmt.Println("      ║ [1] [2] [3] [▲] [◉] ║                        ██║  ██║██║  ██║██████╔╝██║╚██████╔╝      ")
	fmt.Println("      ║ [4] [5] [6] [▼] [●] ║                        ╚═╝  ╚═╝╚═╝  ╚═╝╚═════╝ ╚═╝ ╚═════╝       ")
	fmt.Println("      ║ [7] [8] [9] [◀] [■] ║")
	fmt.Println("      ║ [*] [0] [#] [▶] [⏸] ║")
	fmt.Println("      ╚═════════════════════╝")
	fmt.Println("")
	fmt.Println("╔════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║              Interactive Setup Wizard - v1.0                       ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Check if PostgreSQL is installed locally
	hasLocalPostgres := checkPostgreSQLInstalled()
	var setupMode string

	if !hasLocalPostgres {
		fmt.Println("⚠️  PostgreSQL client (psql) not detected locally.")
		fmt.Println("\nYou have two options:")
		fmt.Println("  1. Install PostgreSQL locally (recommended)")
		fmt.Println("  2. Use an existing remote PostgreSQL server")
		fmt.Println("")
		setupMode = readInput("Choose setup mode (1=local, 2=remote)", "1")

		if setupMode == "1" {
			fmt.Println("\nPlease install PostgreSQL first:")
			fmt.Println("\nLinux (Ubuntu/Debian):")
			fmt.Println("  sudo apt update")
			fmt.Println("  sudo apt install postgresql postgresql-contrib")
			fmt.Println("\nLinux (Fedora/CentOS/RHEL):")
			fmt.Println("  sudo dnf install postgresql postgresql-server")
			fmt.Println("\nmacOS (Homebrew):")
			fmt.Println("  brew install postgresql@16")
			fmt.Println("\nWindows:")
			fmt.Println("  Download from https://www.postgresql.org/download/windows/")
			fmt.Println("\nAfter installation, run this setup wizard again.")
			fmt.Println("")
			return fmt.Errorf("PostgreSQL not installed")
		}

		// Remote mode
		fmt.Println("\n✓ Remote database mode selected")
		fmt.Println("\nNote: Make sure you have:")
		fmt.Println("  - Remote PostgreSQL server accessible from this machine")
		fmt.Println("  - Database and user already created on the remote server")
		fmt.Println("  - Network access allowed (check pg_hba.conf on remote server)")
		fmt.Println("")
	} else {
		fmt.Println("✓ PostgreSQL detected")
		setupMode = "1" // Local mode
	}

	if setupMode == "1" {
		fmt.Println("\nThis wizard will help you set up ThinLine Radio by:")
		fmt.Println("  1. Creating a PostgreSQL database")
		fmt.Println("  2. Creating a database user with appropriate permissions")
		fmt.Println("  3. Generating a configuration file")
	} else {
		fmt.Println("\nThis wizard will help you set up ThinLine Radio by:")
		fmt.Println("  1. Configuring connection to your existing remote database")
		fmt.Println("  2. Generating a configuration file")
	}
	fmt.Println("")

	var pgHost, pgPort, dbName, dbUser, dbPassword string
	var db *sql.DB

	if setupMode == "1" {
		// Local mode - create database and user
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("PostgreSQL Superuser Connection")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		pgHost = readInput("PostgreSQL host", "localhost")
		pgPort = readInput("PostgreSQL port", "5432")
		pgSuperuser := readInput("PostgreSQL superuser", "postgres")
		pgSuperuserPass, err := readPassword("PostgreSQL superuser password: ")
		if err != nil {
			return fmt.Errorf("failed to read password: %v", err)
		}

		// Test connection to PostgreSQL as superuser
		fmt.Print("\n🔄 Testing PostgreSQL connection... ")
		connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=postgres sslmode=disable",
			pgHost, pgPort, pgSuperuser, pgSuperuserPass)

		db, err = sql.Open("postgres", connStr)
		if err != nil {
			fmt.Println("❌")
			return fmt.Errorf("failed to connect to PostgreSQL: %v", err)
		}
		defer db.Close()

		if err := db.Ping(); err != nil {
			fmt.Println("❌")
			return fmt.Errorf("failed to ping PostgreSQL: %v", err)
		}
		fmt.Println("✓")

		// Get new database configuration
		fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("ThinLine Radio Database Configuration")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		dbName = readInput("Database name", "thinline_radio")
		dbUser = readInput("Database username", "thinline_user")
		dbPassword, err = readPassword("Database user password (will be created): ")
		if err != nil {
			return fmt.Errorf("failed to read password: %v", err)
		}
		if dbPassword == "" {
			return fmt.Errorf("database password cannot be empty")
		}

		// Confirm password
		dbPasswordConfirm, err := readPassword("Confirm database user password: ")
		if err != nil {
			return fmt.Errorf("failed to read password: %v", err)
		}
		if dbPassword != dbPasswordConfirm {
			return fmt.Errorf("passwords do not match")
		}
	} else {
		// Remote mode - just get existing database credentials
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("Remote PostgreSQL Database Configuration")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		pgHost = readInput("Remote PostgreSQL host", "")
		pgPort = readInput("Remote PostgreSQL port", "5432")
		dbName = readInput("Existing database name", "thinline_radio")
		dbUser = readInput("Existing database username", "")
		var err error
		dbPassword, err = readPassword("Database password: ")
		if err != nil {
			return fmt.Errorf("failed to read password: %v", err)
		}
		if dbPassword == "" {
			return fmt.Errorf("database password cannot be empty")
		}

		// Test connection to remote database
		fmt.Print("\n🔄 Testing remote database connection... ")
		connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			pgHost, pgPort, dbUser, dbPassword, dbName)

		testDB, err := sql.Open("postgres", connStr)
		if err != nil {
			fmt.Println("❌")
			return fmt.Errorf("failed to connect to remote database: %v", err)
		}
		defer testDB.Close()

		if err := testDB.Ping(); err != nil {
			fmt.Println("❌")
			fmt.Println("\n⚠️  Connection failed. Please check:")
			fmt.Println("  - Remote server is running and accessible")
			fmt.Println("  - Database and user exist on remote server")
			fmt.Println("  - pg_hba.conf allows remote connections")
			fmt.Println("  - Firewall allows port 5432")
			return fmt.Errorf("failed to ping remote database: %v", err)
		}
		fmt.Println("✓")
	}

	// Server configuration
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("Server Configuration")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	serverListen := readInput("Server listen address", "0.0.0.0:3000")

	// Only create database/user in local mode
	if setupMode == "1" && db != nil {
		// Create database
		fmt.Print("\n🔄 Creating database... ")
		var err error
		_, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s OWNER %s", dbName, dbUser))
		if err != nil {
			// Check if database already exists
			if strings.Contains(err.Error(), "already exists") {
				fmt.Println("⚠️  (already exists)")
			} else {
				fmt.Println("❌")
				return fmt.Errorf("failed to create database: %v", err)
			}
		} else {
			fmt.Println("✓")
		}

		// Create user
		fmt.Print("🔄 Creating database user... ")
		_, err = db.Exec(fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s'", dbUser, dbPassword))
		if err != nil {
			// Check if user already exists
			if strings.Contains(err.Error(), "already exists") {
				fmt.Println("⚠️  (already exists)")
				// Update password for existing user
				fmt.Print("🔄 Updating user password... ")
				_, err = db.Exec(fmt.Sprintf("ALTER USER %s WITH PASSWORD '%s'", dbUser, dbPassword))
				if err != nil {
					fmt.Println("❌")
					return fmt.Errorf("failed to update user password: %v", err)
				}
				fmt.Println("✓")
			} else {
				fmt.Println("❌")
				return fmt.Errorf("failed to create user: %v", err)
			}
		} else {
			fmt.Println("✓")
		}

		// Grant privileges
		fmt.Print("🔄 Granting privileges... ")
		_, err = db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s", dbName, dbUser))
		if err != nil {
			fmt.Println("❌")
			return fmt.Errorf("failed to grant privileges: %v", err)
		}
		fmt.Println("✓")
	} else {
		fmt.Println("\n✓ Using existing remote database configuration")
	}

	// Create config file
	fmt.Print("🔄 Creating configuration file... ")
	configContent := fmt.Sprintf(`# ThinLine Radio Configuration
# Generated by interactive setup wizard

# Database Configuration
db_type = postgresql
db_host = %s
db_port = %s
db_name = %s
db_user = %s
db_pass = %s

# Server Configuration
listen = %s

# Optional SSL Configuration (uncomment to enable)
# ssl_listen = 0.0.0.0:3443
# ssl_cert_file = /path/to/cert.pem
# ssl_key_file = /path/to/key.pem
# ssl_auto_cert = yourdomain.com

# Base directory for data storage (optional)
# base_dir = /var/lib/thinline-radio

# Debug logging (optional)
# enable_debug_log = true
`, pgHost, pgPort, dbName, dbUser, dbPassword, serverListen)

	if err := os.WriteFile(configFile, []byte(configContent), 0600); err != nil {
		fmt.Println("❌")
		return fmt.Errorf("failed to write config file: %v", err)
	}
	fmt.Println("✓")

	// Success message
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                      Setup Complete! ✓                             ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	fmt.Printf("Configuration file created: %s\n", configFile)
	fmt.Printf("Database: %s\n", dbName)
	fmt.Printf("User: %s\n", dbUser)
	fmt.Printf("Server: %s\n", serverListen)
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Review and edit the configuration file if needed")
	fmt.Printf("  2. Start the server: ./thinline-radio -config %s\n", configFile)
	fmt.Println("  3. Access admin dashboard: http://localhost:3000/admin")
	fmt.Println("  4. Default admin password: admin (change immediately!)")
	fmt.Println("")

	return nil
}

// shouldRunInteractiveSetup checks if interactive setup should run
func shouldRunInteractiveSetup(config *Config) bool {
	// Check if we're in an interactive terminal first
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false // Not interactive, can't run setup wizard
	}

	// Check if running on Windows (different terminal handling)
	if runtime.GOOS == "windows" {
		// On Windows, check if we can read from stdin
		stat, err := os.Stdin.Stat()
		if err != nil || (stat.Mode()&os.ModeCharDevice) == 0 {
			return false
		}
	}

	// Check if config file exists
	if _, err := os.Stat(config.ConfigFile); err != nil {
		return true // Config doesn't exist, run setup
	}

	// Config file exists, but check if database credentials are configured
	if config.DbName == "" || config.DbUsername == "" || config.DbPassword == "" {
		return true // Config incomplete, run setup
	}

	return false
}
