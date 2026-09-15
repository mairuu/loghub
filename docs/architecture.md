# Architecture

loghub is a demo multi-tenant log management system. Events arrive from syslog, HTTP and files, get normalized to one schema and stored a PostgreSQL database, and are served to a React UI.