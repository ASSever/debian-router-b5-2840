---
name: sqlite-migration
description: Review and verify a SQLite schema change or release upgrade that changes stored data on Debian Router B5-2840.
---

# Exercise the upgrade path

1. Read `backend/internal/database/database.go` and `backend/internal/database/database_test.go`. Keep schema version advancement in the same transaction as the corresponding change. Keep the newer-schema rejection and test the oldest supported schema affected by the change.
2. Use fixture databases to verify fresh initialization, upgrade with representative rows, reopening, and failure without partial migration. For monitoring history and traffic data, exercise persisted timestamps, units, and retention when those semantics change.
3. Before publishing a migration, determine whether the previous binary can open the upgraded database. Read the installer rollback path in `install.sh` and account for schema changes when rolling back binaries. For B5 rollout, use a consistent SQLite backup, such as the backup API or a documented quiesced copy, then check it and rehearse restoration off-device. A plain copy of a live database can omit state in its WAL.
4. Keep real databases and backups local. Record backup and restore evidence in the private setup log, with sanitized acceptance results in the public roadmap.
