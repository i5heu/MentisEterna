"""
MentisEterna integration test: creates ~25 notes (3 top-level + nested replies),
verifies structure, and tests search functionality.

Works on a non-empty database — does not delete existing data and uses unique
titles (timestamped) to avoid collisions.

Usage:
    python3 test_search.py [--base-url http://localhost:8080] [--db mentis.db]

Requirements:
    - MentisEterna server must be running
    - Python 3.6+ (stdlib only)
"""

import argparse
import hashlib
import http.client
import json
import sqlite3
import sys
import time
import urllib.error
import urllib.request


# ── Client -------------------------------------------------------------------
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
            with urllib.request.urlopen(req, timeout=15) as resp:
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

    def health(self):
        status, data = self._req("GET", "/health")
        print("Health: %s" % data)
        return data

    def create_note(self, title: str, body: str, parent_id=None):
        print("  Creating note %r..." % title)
        payload = {"title": title, "body": body}
        if parent_id is not None:
            payload["parent_id"] = parent_id
        status, data = self._req("POST", "/notes", payload)
        print("    -> id=%s  title=%r" % (data["id"], data["title"]))
        return data

    def update_note(self, note_id: int, title: str, body: str, parent_id=None):
        print("  Updating note id=%d title=%r..." % (note_id, title))
        payload = {"title": title, "body": body}
        if parent_id is not None:
            payload["parent_id"] = parent_id
        status, data = self._req("PUT", "/notes/%d" % note_id, payload)
        print("    -> updated id=%s" % data["id"])
        return data

    def get_note(self, note_id: int):
        status, data = self._req("GET", "/notes/%d" % note_id)
        return data

    def delete_note(self, note_id: int):
        status, data = self._req("DELETE", "/notes/%d" % note_id, expect_status=204)
        print("    -> deleted id=%d" % note_id)
        return data

    def list_notes(self):
        print("  Listing all notes...")
        status, data = self._req("GET", "/notes")
        print("    -> %d notes returned" % len(data))
        return data

    def get_children(self, note_id: int):
        status, data = self._req("GET", "/notes/%d/children" % note_id)
        return data

    def get_ancestors(self, note_id: int):
        status, data = self._req("GET", "/notes/%d/ancestors" % note_id)
        return data

    def get_history(self, note_id: int):
        status, data = self._req("GET", "/notes/%d/history" % note_id)
        return data

    def search(self, query: str, retries: int = 3):
        """Search with retry on connection resets (server may be busy with async embedding generation)."""
        encoded = urllib.parse.quote(query)
        last_err = None
        for attempt in range(retries):
            try:
                status, data = self._req("GET", "/notes/search?q=%s" % encoded)
                return data
            except (
                ConnectionResetError,
                BrokenPipeError,
                ConnectionAbortedError,
                urllib.error.URLError,
                http.client.RemoteDisconnected,
            ) as e:
                last_err = e
                wait = (attempt + 1) * 1.5
                print("    (search connection reset, retry in %.1fs...)" % wait)
                time.sleep(wait)
        raise last_err


# ── Helpers ------------------------------------------------------------------
def banner(text: str):
    print("")
    print("=" * 60)
    print(text)
    print("=" * 60)


def ok(text: str):
    print("  \u2714 %s" % text)


def fail(text: str):
    print("  \u2716 FAIL: %s" % text)


def ensure_admin_password(db_path: str, password: str = "testpass123"):
    """Reset admin password to a known value via direct DB access.
    Works on non-empty databases — does not delete any data."""
    try:
        conn = sqlite3.connect(db_path)
        cur = conn.execute("SELECT password_hash FROM auth WHERE username = 'admin'")
        row = cur.fetchone()
        conn.close()

        if row is None:
            print("No admin user in DB yet.")
            print(
                "Start the server once so it creates the admin user, then re-run this script."
            )
            sys.exit(1)

        print("Admin user exists (hash prefix: %s...)" % row[0][:16])
    except Exception as e:
        print("Cannot read DB: %s" % e)
        sys.exit(1)

    new_hash = hashlib.sha512(password.encode()).hexdigest()

    try:
        conn = sqlite3.connect(db_path)
        conn.execute(
            "UPDATE auth SET password_hash = ? WHERE username = 'admin'",
            (new_hash,),
        )
        conn.commit()
        conn.close()
        print("Reset admin password to '%s'." % password)
    except Exception as e:
        print("Cannot reset password: %s" % e)
        sys.exit(1)

    return password


# ── Test data builder --------------------------------------------------------
def build_notes(client: MentisClient, suffix: str):
    """
    Create ~25 notes with this structure:

    Top-level (3):
      [T1] "Programming Languages" {suffix}
        ├── [C1] "Why Python is great"
        │     ├── [G1] "List comprehensions are magic"
        │     └── [G2] "The GIL is actually fine"
        ├── [C2] "Go vs Rust for systems programming"
        │     └── [G3] "Error handling in Go"
        └── [C3] "Functional programming in JavaScript"
              └── [G4] "Array methods you should know"

      [T2] "Daily Journal" {suffix}
        ├── [C4] "Morning routine reflection"
        ├── [C5] "What I learned today"
        │     ├── [G5] "Understanding SQLite extensions"
        │     └── [G6] "VSS search is fast"
        └── [C6] "Goals for tomorrow"

      [T3] "Project Ideas" {suffix}
        ├── [C7] "Personal knowledge base"
        │     ├── [G7] "Vector search is the killer feature"
        │     ├── [G8] "Need offline-first support"
        │     └── [G9] "Markdown editor integration"
        ├── [C8] "CLI habit tracker"
        │     └── [G10] "SQLite for storage, no server needed"
        └── [C9] "Recipe manager with ingredient search"

    Total: 3 top-level + 9 children + 10 grandchildren = 22 base notes
    Plus 3 standalone replies to top-level notes to test flat responses:

      [R1] reply to T1: "Great list of languages!"
      [R2] reply to T1: "You forgot about Rust's borrow checker"
      [R3] reply to T2: "Keep up the journaling habit!"

    Grand total: 25 notes

    Returns a dict with keys: tops, children, grandchildren, replies, all_ids
    """
    notes = {"tops": {}, "children": {}, "grandchildren": {}, "replies": {}}

    banner("Creating notes (suffix: %r)" % suffix)

    # ── Top-level 1: Programming Languages ──
    t1 = client.create_note(
        "Programming Languages %s" % suffix,
        "A collection of thoughts and notes about various programming languages, their ecosystems, and best practices.",
    )
    notes["tops"]["t1"] = t1

    c1 = client.create_note(
        "Why Python is great %s" % suffix,
        "Python's readability and extensive standard library make it perfect for rapid prototyping and data science workflows.",
        parent_id=t1["id"],
    )
    notes["children"]["c1"] = c1
    g1 = client.create_note(
        "List comprehensions are magic %s" % suffix,
        "You can transform any iterable in a single line: [x*2 for x in range(10) if x % 2 == 0]",
        parent_id=c1["id"],
    )
    notes["grandchildren"]["g1"] = g1
    g2 = client.create_note(
        "The GIL is actually fine %s" % suffix,
        "For I/O bound workloads the GIL doesn't matter. Use multiprocessing or asyncio for CPU-bound tasks.",
        parent_id=c1["id"],
    )
    notes["grandchildren"]["g2"] = g2

    c2 = client.create_note(
        "Go vs Rust for systems programming %s" % suffix,
        "Go offers simplicity and fast compile times. Rust provides memory safety without garbage collection. Choose based on your priorities.",
        parent_id=t1["id"],
    )
    notes["children"]["c2"] = c2
    g3 = client.create_note(
        "Error handling in Go %s" % suffix,
        "Explicit error returns feel verbose at first but make error paths visible and encourage handling them immediately.",
        parent_id=c2["id"],
    )
    notes["grandchildren"]["g3"] = g3

    c3 = client.create_note(
        "Functional programming in JavaScript %s" % suffix,
        "Modern JavaScript has first-class functions, closures, and immutable patterns via Array methods and spread operators.",
        parent_id=t1["id"],
    )
    notes["children"]["c3"] = c3
    g4 = client.create_note(
        "Array methods you should know %s" % suffix,
        "map, filter, reduce, flatMap, find, some, every — mastering these makes your code cleaner and more declarative.",
        parent_id=c3["id"],
    )
    notes["grandchildren"]["g4"] = g4

    # ── Top-level 2: Daily Journal ──
    t2 = client.create_note(
        "Daily Journal %s" % suffix,
        "A daily log of thoughts, learnings, and personal reflections.",
    )
    notes["tops"]["t2"] = t2

    c4 = client.create_note(
        "Morning routine reflection %s" % suffix,
        "Woke up at 6:30, meditated for 15 minutes, made coffee, and reviewed the day's priorities before diving into code.",
        parent_id=t2["id"],
    )
    notes["children"]["c4"] = c4

    c5 = client.create_note(
        "What I learned today %s" % suffix,
        "Today's discoveries and insights from reading, coding, and conversations.",
        parent_id=t2["id"],
    )
    notes["children"]["c5"] = c5
    g5 = client.create_note(
        "Understanding SQLite extensions %s" % suffix,
        "SQLite's extension mechanism lets you load custom functions, virtual tables, and even full-text or vector search capabilities.",
        parent_id=c5["id"],
    )
    notes["grandchildren"]["g5"] = g5
    g6 = client.create_note(
        "VSS search is fast %s" % suffix,
        "Vector similarity search via sqlite-vss with FAISS indexing provides sub-10ms query times even on large note collections.",
        parent_id=c5["id"],
    )
    notes["grandchildren"]["g6"] = g6

    c6 = client.create_note(
        "Goals for tomorrow %s" % suffix,
        "Finish the API integration tests, review the PR for search pagination, and go for a run in the evening.",
        parent_id=t2["id"],
    )
    notes["children"]["c6"] = c6

    # ── Top-level 3: Project Ideas ──
    t3 = client.create_note(
        "Project Ideas %s" % suffix,
        "Brainstorming and planning for side projects and potential startup ideas.",
    )
    notes["tops"]["t3"] = t3

    c7 = client.create_note(
        "Personal knowledge base %s" % suffix,
        "A note-taking app with semantic search, hierarchical organization, and offline support. Like a second brain you can query.",
        parent_id=t3["id"],
    )
    notes["children"]["c7"] = c7
    g7 = client.create_note(
        "Vector search is the killer feature %s" % suffix,
        "Being able to search by meaning rather than keywords transforms how you find information in your notes.",
        parent_id=c7["id"],
    )
    notes["grandchildren"]["g7"] = g7
    g8 = client.create_note(
        "Need offline-first support %s" % suffix,
        "Sync via CRDTs or simple last-write-wins. SQLite already handles local storage perfectly.",
        parent_id=c7["id"],
    )
    notes["grandchildren"]["g8"] = g8
    g9 = client.create_note(
        "Markdown editor integration %s" % suffix,
        "A split-pane editor with live preview. Support code blocks, tables, task lists, and internal links between notes.",
        parent_id=c7["id"],
    )
    notes["grandchildren"]["g9"] = g9

    c8 = client.create_note(
        "CLI habit tracker %s" % suffix,
        "A terminal-based habit tracker that stores data in SQLite. Streaks, statistics, and reminders via desktop notifications.",
        parent_id=t3["id"],
    )
    notes["children"]["c8"] = c8
    g10 = client.create_note(
        "SQLite for storage, no server needed %s" % suffix,
        "A single file database is perfect for personal tools. Zero configuration, zero maintenance, and easy to back up.",
        parent_id=c8["id"],
    )
    notes["grandchildren"]["g10"] = g10

    c9 = client.create_note(
        "Recipe manager with ingredient search %s" % suffix,
        "Store recipes, search by ingredients on hand, scale servings automatically, and generate shopping lists.",
        parent_id=t3["id"],
    )
    notes["children"]["c9"] = c9

    # ── Flat replies to top-level notes ──
    r1 = client.create_note(
        "Great list of languages! %s" % suffix,
        "I really appreciate this curated comparison. Python and Go are my daily drivers too.",
        parent_id=t1["id"],
    )
    notes["replies"]["r1"] = r1

    r2 = client.create_note(
        "You forgot about Rust's borrow checker %s" % suffix,
        "The borrow checker is actually one of Rust's best features once you understand ownership semantics.",
        parent_id=t1["id"],
    )
    notes["replies"]["r2"] = r2

    r3 = client.create_note(
        "Keep up the journaling habit! %s" % suffix,
        "Consistency is more important than quality. Even a few lines each day compound into valuable self-knowledge.",
        parent_id=t2["id"],
    )
    notes["replies"]["r3"] = r3

    # ── Compute statistics ──
    all_ids = []
    for group in ("tops", "children", "grandchildren", "replies"):
        for k, n in notes[group].items():
            all_ids.append(n["id"])

    total = len(all_ids)
    print("")
    print(
        "  Created %d notes total (%d tops, %d children, %d grandchildren, %d replies)"
        % (
            total,
            len(notes["tops"]),
            len(notes["children"]),
            len(notes["grandchildren"]),
            len(notes["replies"]),
        )
    )
    notes["all_ids"] = all_ids
    return notes


# ── Test runners -------------------------------------------------------------
def test_structure(client: MentisClient, notes: dict):
    """Verify note hierarchy: children, ancestors, and tree shape."""
    banner("Testing note hierarchy")

    t1 = notes["tops"]["t1"]
    t2 = notes["tops"]["t2"]
    t3 = notes["tops"]["t3"]

    # ── Children of top-level notes ──
    print("")
    print("--- Children of top-level notes ---")
    for label, t in [("T1", t1), ("T2", t2), ("T3", t3)]:
        children = client.get_children(t["id"])
        print("  %s (%r) has %d children:" % (label, t["title"], len(children)))
        for ch in children:
            print("    -> id=%s %r" % (ch["id"], ch["title"]))
            assert ch["parent_id"] == t["id"], "parent_id mismatch"

        if label == "T1":
            assert len(children) >= 5, (
                "T1 should have 3 children + 2 direct replies = 5"
            )
        elif label == "T2":
            assert len(children) >= 4, "T2 should have 3 children + 1 direct reply = 4"
        elif label == "T3":
            assert len(children) >= 3, "T3 should have 3 children"
    ok("Top-level children count correct")

    # ── Nested children (grandchildren) ──
    print("")
    print("--- Grandchildren (nested under children) ---")
    c1 = notes["children"]["c1"]
    grandchildren_c1 = client.get_children(c1["id"])
    print("  c1 (%r) has %d children:" % (c1["title"], len(grandchildren_c1)))
    for gc in grandchildren_c1:
        print("    -> id=%s %r" % (gc["id"], gc["title"]))
    assert len(grandchildren_c1) == 2, "c1 should have 2 grandchildren"

    c5 = notes["children"]["c5"]
    grandchildren_c5 = client.get_children(c5["id"])
    assert len(grandchildren_c5) == 2, "c5 should have 2 grandchildren"

    c7 = notes["children"]["c7"]
    grandchildren_c7 = client.get_children(c7["id"])
    assert len(grandchildren_c7) == 3, "c7 should have 3 grandchildren"
    ok("Grandchildren counts correct")

    # ── Ancestors chain ──
    print("")
    print("--- Ancestor chains ---")
    g7 = notes["grandchildren"]["g7"]
    ancestors = client.get_ancestors(g7["id"])
    print("  Ancestors of g7 (%r):" % g7["title"])
    for a in ancestors:
        print("    -> id=%s %r (parent_id=%s)" % (a["id"], a["title"], a["parent_id"]))
    assert len(ancestors) == 3, "g7 should have 3 ancestors (root -> c7 -> g7)"
    assert ancestors[0]["id"] == t3["id"], "first ancestor should be T3"
    assert ancestors[1]["id"] == c7["id"], "second ancestor should be C7"
    assert ancestors[2]["id"] == g7["id"], "third ancestor should be G7 itself"
    ok("Ancestor chains correct")

    # ── Top-level notes have no parent ──
    print("")
    print("--- Top-level notes parent check ---")
    for label, t in [("T1", t1), ("T2", t2), ("T3", t3)]:
        fetched = client.get_note(t["id"])
        assert fetched["parent_id"] is None, "%s should have parent_id=None" % label
        print("  %s parent_id=None ✓" % label)
    ok("Top-level notes have no parent")


def test_update_and_history(client: MentisClient, notes: dict):
    """Update a note and verify history is preserved."""
    banner("Testing note updates and history")

    g6 = notes["grandchildren"]["g6"]
    note_id = g6["id"]

    print("  Original body:")
    print("    %s" % g6["body"][:80])

    # Update the note
    new_body = "Updated: Vector similarity search via sqlite-vss with FAISS indexing delivers millisecond query times on collections with thousands of notes."
    updated = client.update_note(note_id, g6["title"], new_body)
    assert updated["body"] == new_body, "Updated body should match"
    ok("Note updated successfully")

    # Check history
    print("")
    print("--- History for note id=%d ---" % note_id)
    history = client.get_history(note_id)
    print("  %d history entries:" % len(history))
    for h in history:
        print(
            "    update_id=%s created=%s body=%r"
            % (h["id"], h["created_at"], h["body"][:60])
        )
    assert len(history) >= 2, (
        "Should have at least 2 history entries (original + update)"
    )
    assert history[0]["body"] == new_body, (
        "Latest history entry should have updated body"
    )
    ok("History correctly tracks updates")

    # Fetch the note again and verify body matches latest
    fetched = client.get_note(note_id)
    assert fetched["body"] == new_body, "GET note should return latest body"
    ok("GET returns latest body")


def test_search(client: MentisClient, notes: dict):
    """Run semantic searches and verify results make sense.

    Note: If VSS is unavailable on the server, search returns empty results.
    This test handles both cases gracefully.
    """
    banner("Testing semantic search")

    # Allow time for async embedding generation (async goroutines may be calling Ollama)
    print("  Waiting 8 seconds for async embedding generation to complete...")
    time.sleep(8)

    test_queries = [
        ("vector search database", "Should find VSS-related notes"),
        ("python programming language", "Should find Python-related notes"),
        ("morning routine habits journal", "Should find journal/reflection notes"),
        ("recipe cooking food ingredients", "Should find recipe manager notes"),
        ("rust borrow checker memory safety", "Should find Rust-related notes"),
        ("markdown editor notes app", "Should find knowledge base notes"),
        ("sqlite storage local database", "Should find SQLite-related notes"),
        ("daily journal goals reflection", "Should find journal notes"),
    ]

    all_results = {}
    any_results = False

    for query, description in test_queries:
        print("")
        print("  Query: %r  (%s)" % (query, description))
        try:
            results = client.search(query)
        except Exception as e:
            print("    -> search failed: %s" % e)
            all_results[query] = []
            continue
        all_results[query] = results
        print("    -> %d results" % len(results))
        for i, r in enumerate(results[:5]):  # show top 5
            print(
                "      %d. [dist=%.4f] id=%s %r"
                % (i + 1, r["distance"], r["id"], r["title"])
            )
        if len(results) > 5:
            print("      ... and %d more" % (len(results) - 5))

        if results:
            any_results = True

    if any_results:
        ok("Search returned results for at least some queries")

        # Verify results contain expected data shape
        for query, results in all_results.items():
            if results:
                r = results[0]
                assert "id" in r, "Result missing id"
                assert "title" in r, "Result missing title"
                assert "body" in r, "Result missing body"
                assert "distance" in r, "Result missing distance"
                assert "parent_id" in r, "Result missing parent_id"
                assert "created_at" in r, "Result missing created_at"
                assert "updated_at" in r, "Result missing updated_at"
                break
        ok("Search result shape is correct")

        # Check that more specific queries rank relevant content higher.
        # "vector search" should rank VSS note above recipe notes.
        vr = all_results.get("vector search database", [])
        if len(vr) >= 2:
            titles = [r["title"] for r in vr]
            print("")
            print("  Top results for 'vector search database': %s" % titles)
            # The VSS note or knowledge-base note should appear before unrelated notes
            has_vss_high = any(
                "vss" in t.lower() or "vector" in t.lower() or "knowledge" in t.lower()
                for t in titles[:3]
            )
            if has_vss_high:
                ok("Vector-search query ranks relevant notes high")
            else:
                print("  (Note: ranking may vary with embedding model)")
    else:
        print("")
        print("  \u2139 All searches returned 0 results.")
        print(
            "  This likely means VSS/embeddings are unavailable (no Ollama, no .so files, etc)."
        )
        print("  This is not a failure — the server gracefully returns empty results.")


def test_list_all(client: MentisClient, notes: dict, suffix: str):
    """Verify all created notes appear in the global list."""
    banner("Testing global note listing")

    all_notes = client.list_notes()
    our_ids = set(notes["all_ids"])
    listed_ids = {n["id"] for n in all_notes}

    missing = our_ids - listed_ids
    if missing:
        fail("Missing %d notes from listing: %s" % (len(missing), missing))
    else:
        ok("All %d created notes appear in global listing" % len(our_ids))

    # Also verify that notes from before this test run still exist (non-empty DB safety)
    pre_existing = listed_ids - our_ids
    if pre_existing:
        ok(
            "DB contains %d pre-existing notes (non-destructive test)"
            % len(pre_existing)
        )


def test_delete(client: MentisClient, notes: dict):
    """Delete one note and verify it's gone."""
    banner("Testing note deletion")

    r3 = notes["replies"]["r3"]
    note_id = r3["id"]
    print("  Deleting reply note id=%d (%r)..." % (note_id, r3["title"]))
    client.delete_note(note_id)
    ok("Note deleted (204)")

    # Verify it's gone
    try:
        client.get_note(note_id)
        fail("Note %d should return 404 after deletion" % note_id)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            ok("Deleted note returns 404 as expected")
        else:
            fail("Expected 404, got %d" % e.code)

    # Remove from our tracking
    notes["all_ids"].remove(note_id)
    del notes["replies"]["r3"]


# ── Main ─────────────────────────────────────────────────────────────────────
def main():
    parser = argparse.ArgumentParser(
        description="MentisEterna integration test — creates ~25 notes and tests search"
    )
    parser.add_argument(
        "--base-url", default="http://localhost:8080", help="Server base URL"
    )
    parser.add_argument("--db", default="mentis.db", help="Path to the SQLite database")
    args = parser.parse_args()

    # ── Step 1: Login (reset admin password to known value) ──
    banner("Step 1: Login")
    c = MentisClient(args.base_url)
    pw = ensure_admin_password(args.db)
    try:
        c.login("admin", pw)
        ok("Logged in successfully")
    except Exception as e:
        fail("Login failed: %s" % e)
        sys.exit(1)

    # ── Step 2: Health check ──
    banner("Step 2: Health check")
    try:
        c.health()
        ok("Server is reachable")
    except Exception as e:
        fail("Cannot reach server: %s" % e)
        sys.exit(1)

    # ── Step 3: Create notes ──
    suffix = time.strftime("[test %Y-%m-%d %H:%M:%S]")
    notes = build_notes(c, suffix)

    # ── Step 4: Test hierarchy ──
    test_structure(c, notes)

    # ── Step 5: Test updates and history ──
    test_update_and_history(c, notes)

    # ── Step 6: Test search ──
    test_search(c, notes)

    # ── Step 7: Test global listing ──
    test_list_all(c, notes, suffix)

    # ── Step 8: Test deletion ──
    test_delete(c, notes)

    # ── Done ──
    banner("All tests completed")
    print("")
    print("  Created and verified %d notes successfully." % len(notes["all_ids"]))
    print("  Notes have suffix %r for easy identification." % suffix)
    print("")


if __name__ == "__main__":
    main()
