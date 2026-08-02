# Security policy

Report vulnerabilities through GitHub private vulnerability reporting. Do not publish production
URLs, header values, database credentials, certificates, or exploit details in an issue.

The service has no authentication and is designed for a trusted operations network. Keep the
status page, API, and Prometheus endpoint behind an authenticated reverse proxy in production.
Treat YAML headers as secrets, load them from environment variables, restrict configuration file
permissions, and never expose the demo control port outside localhost.

The latest release on `main` receives security fixes.

