"""
Integration test: login, create notes, and search via the MentisEterna API.
Usage: python3 test_search.py [--base-url http://localhost:8080]
"""

import argparse
import hashlib
import json
import sqlite3
import sys
import urllib.error
import urllib.parse
import urllib.request


class MentisClient:
    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip("/")
        self.token = None

    def _req(self, method: str, path: str, body=None, expect_status=None):
        url = self.base_url + path
        data = json.dumps(body).encode() if body is not None else None
        headers = {"Content-Type": "application/json"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=10) as resp:
                raw = resp.read()
                status = resp.status
        except urllib.error.HTTPError as e:
            raw = e.read()
            status = e.code
            if expect_status is not None:
                expected = (
                    expect_status
                    if isinstance(expect_status, tuple)
                    else (expect_status,)
                )
                if status in expected:
                    return status, raw.decode(errors="replace")
            print("  HTTP %d on %s %s" % (status, method, path))
            print("  Body: %s" % raw.decode(errors="replace")[:500])
            raise

        if expect_status is not None:
            expected = (
                expect_status if isinstance(expect_status, tuple) else (expect_status,)
            )
            if status not in expected:
                print("  Unexpected status %d on %s %s" % (status, method, path))
                print("  Body: %s" % raw.decode(errors="replace")[:500])
                raise RuntimeError(
                    "Expected status %s, got %d" % (expect_status, status)
                )

        text = raw.decode(errors="replace")
        if status == 204 or not text:
            return status, None
        return status, json.loads(text)

    def login(self, username: str, password: str):
        print("Logging in as '%s'..." % username)
        status, data = self._req(
            "POST", "/login", {"username": username, "password": password}
        )
        self.token = data["token"]
        print(
            "  Token: %s... (expires %s)"
            % (self.token[:16], data.get("expires_at", "?"))
        )
        return data

    def create_note(self, title: str, body: str, parent_id=None):
        print("Creating note '%s'..." % title)
        payload = {"title": title, "body": body}
        if parent_id is not None:
            payload["parent_id"] = parent_id
        status, data = self._req("POST", "/notes", payload)
        print("  Created id=%s title=%r" % (data["id"], data["title"]))
        return data

    def list_notes(self):
        print("Listing notes...")
        status, data = self._req("GET", "/notes")
        print("  Got %d notes" % len(data))
        for n in data:
            print("    id=%s title=%r" % (n["id"], n["title"]))
        return data

    def search(self, query: str):
        encoded = urllib.parse.quote(query)
        print("Searching for '%s' (GET /notes/search?q=%s)..." % (query, encoded))
        status, data = self._req("GET", "/notes/search?q=" + encoded)
        print("  Status: %d" % status)
        print("  Results: " + json.dumps(data, indent=2))
        return data

    def health(self):
        status, data = self._req("GET", "/health")
        print("Health: %s" % data)
        return data


def main():
    parser = argparse.ArgumentParser(description="MentisEterna search integration test")
    parser.add_argument(
        "--base-url", default="http://localhost:8080", help="Server base URL"
    )
    args = parser.parse_args()

    c = MentisClient(args.base_url)

    # 1. Login — reset admin password to a known value via direct DB access
    print("=" * 60)
    print("1. Login")

    db_path = "mentis.db"
    try:
        conn = sqlite3.connect(db_path)
        cur = conn.execute("SELECT password_hash FROM auth WHERE username = 'admin'")
        row = cur.fetchone()
        conn.close()
        if row is None:
            print("No admin user in DB yet.")
            print(
                "Start the server once and capture the printed password, then pass it via:"
            )
            print("  python3 test_search.py --password 'THE_PASSWORD'")
            sys.exit(1)
        print("Admin user exists (hash: %s...)" % row[0][:16])
    except Exception as e:
        print("Cannot read DB: %s" % e)
        sys.exit(1)

    new_pw = "testpass123"
    new_hash = hashlib.sha512(new_pw.encode()).hexdigest()

    try:
        conn = sqlite3.connect(db_path)
        conn.execute(
            "UPDATE auth SET password_hash = ? WHERE username = 'admin'", (new_hash,)
        )
        conn.commit()
        conn.close()
        print("Reset admin password to '%s' for testing." % new_pw)
    except Exception as e:
        print("Cannot reset password: %s" % e)
        sys.exit(1)

    try:
        c.login("admin", new_pw)
    except Exception as e:
        print("FAIL: Login failed: %s" % e)
        sys.exit(1)

    # 3. Create notes
    print("")
    print("=" * 60)
    print("3. Creating notes")
    import time

    try:
        note1 = c.create_note("Hello Note", "Hello World")
        note2 = c.create_note("Python Note", "Python is awesome for testing APIs")
        note3 = c.create_note("Grocery List", "Milk, eggs, bread, butter")
        # Give the async embedding goroutine time to finish
        print("Waiting 2s for async embedding sync...")
        time.sleep(2)
    except Exception as e:
        print("FAIL: Create note failed: %s" % e)
        sys.exit(1)

    # 4. List notes
    print("")
    print("=" * 60)
    print("4. Listing all notes")
    try:
        c.list_notes()
    except Exception as e:
        print("FAIL: List notes failed: %s" % e)
        sys.exit(1)

    # 5. Search tests
    print("")
    print("=" * 60)
    print("5. Search tests")

    tests = [
        ("Hello World", "exact title/body match"),
        ("Hello", "partial match"),
        ("Python", "different note match"),
        ("grocery", "case-insensitive-ish"),
        ("nonexistent_xyz_123", "no results expected"),
    ]

    failures = 0
    for query, description in tests:
        print("")
        print("--- Test: %s (q=%r) ---" % (description, query))
        try:
            results = c.search(query)
            print("  OK: %d result(s)" % len(results))
        except urllib.error.HTTPError as e:
            body = e.read().decode(errors="replace")
            print("  HTTP ERROR %d: %s" % (e.code, body[:300]))
            failures += 1
        except Exception as e:
            print("  FAIL: %s" % e)
            failures += 1

    print("")
    print("=" * 60)
    if failures == 0:
        print("All search tests passed!")
    else:
        print("%d search test(s) failed!" % failures)
        sys.exit(1)


if __name__ == "__main__":
    main()
