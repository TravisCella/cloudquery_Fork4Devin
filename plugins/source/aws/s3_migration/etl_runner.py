#!/usr/bin/env python3
"""ETL Runner: Migrates aws_s3_buckets from deprecated flat-column schema to
the new nested child-table schema introduced in cloudquery/cloudquery#15395.

Deprecated columns on aws_s3_buckets:
    block_public_acls, block_public_policy, ignore_public_acls,
    restrict_public_buckets, policy, versioning_status, versioning_mfa_delete,
    logging_target_bucket, logging_target_prefix, replication_role,
    replication_rules, ownership_controls

New child tables:
    aws_s3_bucket_public_access_blocks, aws_s3_bucket_policies,
    aws_s3_bucket_versionings, aws_s3_bucket_loggings,
    aws_s3_bucket_replications, aws_s3_bucket_ownership_controls

Usage:
    python etl_runner.py --db-url postgresql://user:pass@host:5432/dbname
    python etl_runner.py --db-url postgresql://... --dry-run
    python etl_runner.py --db-url postgresql://... --rollback
"""

import argparse
import os
import sys
from pathlib import Path

MIGRATION_DIR = Path(__file__).parent
MIGRATE_SQL = MIGRATION_DIR / "migrate_s3_schema.sql"
ROLLBACK_SQL = MIGRATION_DIR / "rollback_s3_schema.sql"

DEPRECATED_COLUMNS = [
    "block_public_acls",
    "block_public_policy",
    "ignore_public_acls",
    "restrict_public_buckets",
    "policy",
    "versioning_status",
    "versioning_mfa_delete",
    "logging_target_bucket",
    "logging_target_prefix",
    "replication_role",
    "replication_rules",
    "ownership_controls",
]

CHILD_TABLES = [
    "aws_s3_bucket_public_access_blocks",
    "aws_s3_bucket_policies",
    "aws_s3_bucket_versionings",
    "aws_s3_bucket_loggings",
    "aws_s3_bucket_replications",
    "aws_s3_bucket_ownership_controls",
]


def get_connection(db_url):
    try:
        import psycopg2
    except ImportError:
        print("ERROR: psycopg2 is required. Install with: pip install psycopg2-binary")
        sys.exit(1)
    return psycopg2.connect(db_url)


def check_deprecated_columns_exist(conn):
    cur = conn.cursor()
    cur.execute(
        "SELECT column_name FROM information_schema.columns "
        "WHERE table_name = 'aws_s3_buckets' AND column_name = ANY(%s)",
        (DEPRECATED_COLUMNS,),
    )
    found = {row[0] for row in cur.fetchall()}
    cur.close()
    return found


def check_child_tables_exist(conn):
    cur = conn.cursor()
    cur.execute(
        "SELECT table_name FROM information_schema.tables "
        "WHERE table_name = ANY(%s)",
        (CHILD_TABLES,),
    )
    found = {row[0] for row in cur.fetchall()}
    cur.close()
    return found


def count_rows(conn, table):
    cur = conn.cursor()
    cur.execute(f"SELECT count(*) FROM {table}")  # noqa: S608
    count = cur.fetchone()[0]
    cur.close()
    return count


def pre_flight_check(conn):
    print("--- Pre-flight checks ---")

    deprecated = check_deprecated_columns_exist(conn)
    if deprecated:
        print(f"  Deprecated columns found on aws_s3_buckets: {sorted(deprecated)}")
    else:
        print("  No deprecated columns found on aws_s3_buckets (already migrated or fresh sync).")

    children = check_child_tables_exist(conn)
    missing = set(CHILD_TABLES) - children
    if missing:
        print(f"  WARNING: Missing child tables (will be created by next sync): {sorted(missing)}")
        return False

    print(f"  Child tables present: {sorted(children)}")

    bucket_count = count_rows(conn, "aws_s3_buckets")
    print(f"  aws_s3_buckets row count: {bucket_count}")

    return len(deprecated) > 0


def run_sql_file(conn, sql_path, dry_run=False):
    sql = sql_path.read_text()
    if dry_run:
        print(f"\n--- DRY RUN: Would execute {sql_path.name} ---")
        print(sql)
        return True

    print(f"\n--- Executing {sql_path.name} ---")
    cur = conn.cursor()
    try:
        cur.execute(sql)
        conn.commit()
        print(f"  {sql_path.name} completed successfully.")
        return True
    except Exception as e:
        conn.rollback()
        print(f"  ERROR executing {sql_path.name}: {e}")
        return False
    finally:
        cur.close()


def post_migration_verify(conn):
    print("\n--- Post-migration verification ---")

    deprecated = check_deprecated_columns_exist(conn)
    if deprecated:
        print(f"  WARNING: Deprecated columns still present: {sorted(deprecated)}")
    else:
        print("  Deprecated columns successfully removed from aws_s3_buckets.")

    for table in CHILD_TABLES:
        try:
            count = count_rows(conn, table)
            print(f"  {table}: {count} rows")
        except Exception:
            print(f"  {table}: table not found or empty")


def main():
    parser = argparse.ArgumentParser(
        description="ETL migration for aws_s3_buckets deprecated flat columns"
    )
    parser.add_argument(
        "--db-url",
        default=os.environ.get("CQ_DSN", ""),
        help="PostgreSQL connection string (or set CQ_DSN env var)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print the SQL that would be executed without running it",
    )
    parser.add_argument(
        "--rollback",
        action="store_true",
        help="Roll back the migration (restore deprecated flat columns)",
    )
    args = parser.parse_args()

    if not args.db_url:
        print("ERROR: --db-url or CQ_DSN environment variable is required.")
        sys.exit(1)

    conn = get_connection(args.db_url)

    try:
        needs_migration = pre_flight_check(conn)

        if args.rollback:
            success = run_sql_file(conn, ROLLBACK_SQL, dry_run=args.dry_run)
            if success and not args.dry_run:
                print("\nRollback complete.")
            return 0 if success else 1

        if not needs_migration and not args.dry_run:
            print("\nNo migration needed: deprecated columns not found.")
            return 0

        success = run_sql_file(conn, MIGRATE_SQL, dry_run=args.dry_run)

        if success and not args.dry_run:
            post_migration_verify(conn)
            print("\nMigration complete.")

        return 0 if success else 1
    finally:
        conn.close()


if __name__ == "__main__":
    sys.exit(main())
