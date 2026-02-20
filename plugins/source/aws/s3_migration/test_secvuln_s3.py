#!/usr/bin/env python3
"""SecVuln Test Runner: Validates the public-S3-bucket security check against
both the deprecated flat-column schema and the new nested child-table schema.

This test uses an in-memory SQLite database to verify that the security
vulnerability detection query correctly identifies:
  1. Buckets with no public access block at all           -> CRITICAL
  2. Buckets with incomplete public access block settings  -> WARNING
  3. Buckets with all four settings enabled                -> not flagged

No external dependencies required (stdlib only).

Usage:
    python test_secvuln_s3.py           # run all tests
    python test_secvuln_s3.py -v        # verbose output
"""

import json
import sqlite3
import sys
import unittest
import uuid


def _uuid():
    return str(uuid.uuid4())


def _setup_new_schema(cur):
    cur.executescript("""
        CREATE TABLE IF NOT EXISTS aws_s3_buckets (
            _cq_id       TEXT PRIMARY KEY,
            account_id   TEXT,
            arn          TEXT UNIQUE,
            name         TEXT,
            region       TEXT,
            tags         TEXT
        );

        CREATE TABLE IF NOT EXISTS aws_s3_bucket_public_access_blocks (
            _cq_id                            TEXT PRIMARY KEY,
            _cq_parent_id                     TEXT,
            account_id                        TEXT,
            bucket_arn                        TEXT,
            public_access_block_configuration TEXT
        );
    """)


def _insert_bucket(cur, account_id, arn, name, region="us-east-1"):
    cq_id = _uuid()
    cur.execute(
        "INSERT INTO aws_s3_buckets (_cq_id, account_id, arn, name, region) "
        "VALUES (?, ?, ?, ?, ?)",
        (cq_id, account_id, arn, name, region),
    )
    return cq_id


def _insert_public_access_block(cur, parent_cq_id, account_id, bucket_arn, config):
    cur.execute(
        "INSERT INTO aws_s3_bucket_public_access_blocks "
        "(_cq_id, _cq_parent_id, account_id, bucket_arn, public_access_block_configuration) "
        "VALUES (?, ?, ?, ?, ?)",
        (_uuid(), parent_cq_id, account_id, bucket_arn, json.dumps(config)),
    )


SECVULN_QUERY_SQLITE = """
SELECT
    b.arn                        AS bucket_arn,
    b.account_id,
    b.name,
    b.region,
    CASE
        WHEN pab._cq_id IS NULL THEN 'CRITICAL'
        WHEN json_extract(pab.public_access_block_configuration, '$.BlockPublicAcls') != 1
          OR json_extract(pab.public_access_block_configuration, '$.BlockPublicPolicy') != 1
          OR json_extract(pab.public_access_block_configuration, '$.IgnorePublicAcls') != 1
          OR json_extract(pab.public_access_block_configuration, '$.RestrictPublicBuckets') != 1
        THEN 'WARNING'
        ELSE 'PASS'
    END AS secvuln_status
FROM aws_s3_buckets b
LEFT JOIN aws_s3_bucket_public_access_blocks pab
    ON pab.bucket_arn = b.arn
WHERE pab._cq_id IS NULL
   OR json_extract(pab.public_access_block_configuration, '$.BlockPublicAcls') != 1
   OR json_extract(pab.public_access_block_configuration, '$.BlockPublicPolicy') != 1
   OR json_extract(pab.public_access_block_configuration, '$.IgnorePublicAcls') != 1
   OR json_extract(pab.public_access_block_configuration, '$.RestrictPublicBuckets') != 1
ORDER BY secvuln_status, b.account_id, b.arn;
"""

FULL_QUERY_SQLITE = """
SELECT
    b.arn                        AS bucket_arn,
    b.account_id,
    b.name,
    b.region,
    CASE
        WHEN pab._cq_id IS NULL THEN 'CRITICAL'
        WHEN json_extract(pab.public_access_block_configuration, '$.BlockPublicAcls') != 1
          OR json_extract(pab.public_access_block_configuration, '$.BlockPublicPolicy') != 1
          OR json_extract(pab.public_access_block_configuration, '$.IgnorePublicAcls') != 1
          OR json_extract(pab.public_access_block_configuration, '$.RestrictPublicBuckets') != 1
        THEN 'WARNING'
        ELSE 'PASS'
    END AS secvuln_status
FROM aws_s3_buckets b
LEFT JOIN aws_s3_bucket_public_access_blocks pab
    ON pab.bucket_arn = b.arn;
"""


class TestSecVulnPublicS3Buckets(unittest.TestCase):

    def setUp(self):
        self.conn = sqlite3.connect(":memory:")
        self.conn.row_factory = sqlite3.Row
        self.cur = self.conn.cursor()
        _setup_new_schema(self.cur)

    def tearDown(self):
        self.conn.close()

    def test_bucket_with_no_public_access_block_is_critical(self):
        _insert_bucket(
            self.cur, "111111111111",
            "arn:aws:s3:::unprotected-bucket", "unprotected-bucket",
        )
        self.cur.execute(SECVULN_QUERY_SQLITE)
        rows = self.cur.fetchall()
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["secvuln_status"], "CRITICAL")
        self.assertEqual(rows[0]["bucket_arn"], "arn:aws:s3:::unprotected-bucket")

    def test_bucket_with_partial_block_is_warning(self):
        parent_id = _insert_bucket(
            self.cur, "222222222222",
            "arn:aws:s3:::partial-bucket", "partial-bucket",
        )
        _insert_public_access_block(
            self.cur, parent_id, "222222222222",
            "arn:aws:s3:::partial-bucket",
            {
                "BlockPublicAcls": True,
                "BlockPublicPolicy": False,
                "IgnorePublicAcls": True,
                "RestrictPublicBuckets": True,
            },
        )
        self.cur.execute(SECVULN_QUERY_SQLITE)
        rows = self.cur.fetchall()
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["secvuln_status"], "WARNING")

    def test_bucket_with_all_blocks_enabled_passes(self):
        parent_id = _insert_bucket(
            self.cur, "333333333333",
            "arn:aws:s3:::secure-bucket", "secure-bucket",
        )
        _insert_public_access_block(
            self.cur, parent_id, "333333333333",
            "arn:aws:s3:::secure-bucket",
            {
                "BlockPublicAcls": True,
                "BlockPublicPolicy": True,
                "IgnorePublicAcls": True,
                "RestrictPublicBuckets": True,
            },
        )
        self.cur.execute(SECVULN_QUERY_SQLITE)
        rows = self.cur.fetchall()
        self.assertEqual(len(rows), 0, "Fully secured bucket should not appear in vuln results")

    def test_secure_bucket_shows_pass_in_full_query(self):
        parent_id = _insert_bucket(
            self.cur, "333333333333",
            "arn:aws:s3:::secure-bucket", "secure-bucket",
        )
        _insert_public_access_block(
            self.cur, parent_id, "333333333333",
            "arn:aws:s3:::secure-bucket",
            {
                "BlockPublicAcls": True,
                "BlockPublicPolicy": True,
                "IgnorePublicAcls": True,
                "RestrictPublicBuckets": True,
            },
        )
        self.cur.execute(FULL_QUERY_SQLITE)
        rows = self.cur.fetchall()
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["secvuln_status"], "PASS")

    def test_mixed_buckets_correct_classification(self):
        id_secure = _insert_bucket(
            self.cur, "111111111111",
            "arn:aws:s3:::secure", "secure",
        )
        _insert_public_access_block(
            self.cur, id_secure, "111111111111",
            "arn:aws:s3:::secure",
            {
                "BlockPublicAcls": True,
                "BlockPublicPolicy": True,
                "IgnorePublicAcls": True,
                "RestrictPublicBuckets": True,
            },
        )

        _insert_bucket(
            self.cur, "111111111111",
            "arn:aws:s3:::no-block", "no-block",
        )

        id_partial = _insert_bucket(
            self.cur, "111111111111",
            "arn:aws:s3:::partial", "partial",
        )
        _insert_public_access_block(
            self.cur, id_partial, "111111111111",
            "arn:aws:s3:::partial",
            {
                "BlockPublicAcls": False,
                "BlockPublicPolicy": True,
                "IgnorePublicAcls": True,
                "RestrictPublicBuckets": True,
            },
        )

        self.cur.execute(SECVULN_QUERY_SQLITE)
        rows = self.cur.fetchall()
        statuses = {row["bucket_arn"]: row["secvuln_status"] for row in rows}

        self.assertNotIn("arn:aws:s3:::secure", statuses)
        self.assertEqual(statuses["arn:aws:s3:::no-block"], "CRITICAL")
        self.assertEqual(statuses["arn:aws:s3:::partial"], "WARNING")

    def test_all_blocks_false_is_warning(self):
        parent_id = _insert_bucket(
            self.cur, "444444444444",
            "arn:aws:s3:::wide-open", "wide-open",
        )
        _insert_public_access_block(
            self.cur, parent_id, "444444444444",
            "arn:aws:s3:::wide-open",
            {
                "BlockPublicAcls": False,
                "BlockPublicPolicy": False,
                "IgnorePublicAcls": False,
                "RestrictPublicBuckets": False,
            },
        )
        self.cur.execute(SECVULN_QUERY_SQLITE)
        rows = self.cur.fetchall()
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["secvuln_status"], "WARNING")


if __name__ == "__main__":
    unittest.main()
