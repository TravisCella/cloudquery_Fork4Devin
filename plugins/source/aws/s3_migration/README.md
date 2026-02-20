# AWS S3 Schema ETL Migration

## Background

The `aws_s3_buckets` table in the CloudQuery AWS source plugin underwent a breaking schema change ([#15395](https://github.com/cloudquery/cloudquery/pull/15395)). Flat columns on `aws_s3_buckets` were deprecated and replaced with dedicated child tables joined via `bucket_arn`.

### Deprecated Columns (removed from `aws_s3_buckets`)

| Column | Type |
|---|---|
| `block_public_acls` | `bool` |
| `block_public_policy` | `bool` |
| `ignore_public_acls` | `bool` |
| `restrict_public_buckets` | `bool` |
| `policy` | `json` |
| `versioning_status` | `utf8` |
| `versioning_mfa_delete` | `utf8` |
| `logging_target_bucket` | `utf8` |
| `logging_target_prefix` | `utf8` |
| `replication_role` | `utf8` |
| `replication_rules` | `json` |
| `ownership_controls` | `list<utf8>` |

### New Child Tables

| Child Table | Join Key | Key Columns |
|---|---|---|
| `aws_s3_bucket_public_access_blocks` | `bucket_arn` | `public_access_block_configuration` (json) |
| `aws_s3_bucket_policies` | `bucket_arn` | `policy_json`, `policy` |
| `aws_s3_bucket_versionings` | `bucket_arn` | `status`, `mfa_delete` |
| `aws_s3_bucket_loggings` | `bucket_arn` | `logging_enabled` (json) |
| `aws_s3_bucket_replications` | `bucket_arn` | `replication_configuration` (json) |
| `aws_s3_bucket_ownership_controls` | `bucket_arn` | `object_ownership` |

## Files

| File | Purpose |
|---|---|
| `migrate_s3_schema.sql` | Forward migration: extracts deprecated flat columns into child tables, then drops the deprecated columns |
| `rollback_s3_schema.sql` | Reverse migration: re-adds deprecated columns and backfills from child tables |
| `etl_runner.py` | Python orchestrator for running the migration with pre-flight checks and verification |
| `secvuln_public_s3_buckets.sql` | Security vulnerability query: detects public S3 buckets using the **new** nested schema |
| `test_secvuln_s3.py` | Unit tests validating the SecVuln check against all classification scenarios |

## Usage

### Run the ETL migration

```bash
# Dry run (prints SQL without executing)
python etl_runner.py --db-url postgresql://user:pass@host:5432/cloudquery --dry-run

# Execute migration
python etl_runner.py --db-url postgresql://user:pass@host:5432/cloudquery

# Rollback if needed
python etl_runner.py --db-url postgresql://user:pass@host:5432/cloudquery --rollback
```

### Run SecVuln tests

```bash
python test_secvuln_s3.py -v
```

### Run the SecVuln query against your database

```bash
psql -d cloudquery -f secvuln_public_s3_buckets.sql
```
