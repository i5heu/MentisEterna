<script lang="ts">
    import { onMount } from "svelte";
    let name: string = "World";
    let messages: string[] = [];
    let input: string = "";

    type Note = {
        content: string;
        hash: string;
        level: string;
    };

    let notes: Note[] = [];

    async function handleInput() {
        if (input.startsWith("/new ")) {
            const content = input.slice(5);
            // Send POST request to store new note
            await fetch("http://localhost:8188/note", {
                method: "POST",
                headers: { "Content-Type": "text/plain" },
                body: content,
            });
            messages.push(`Note created: ${content}`);
        } else if (input.trim() === "/list") {
            // Send GET request to retrieve notes
            const response = await fetch("http://localhost:8188/note");
            notes = await response.json();
        } else {
            console.log(`You: ${input}`);
            messages.push(`You: ${input}`);
        }
        input = "";
        messages = messages;
    }
</script>

<h1>Hello {name}!</h1>

<div class="chat-container">
    <div class="messages">
        {#each notes as note}
            <div class="note-card">
                <div class="note-content">{note.content}</div>
                {#if note.hash || note.level}
                    <div class="note-meta">
                        {#if note.hash}<span class="hash">{note.hash}</span
                            >{/if}
                        {#if note.level}<span class="level">{note.level}</span
                            >{/if}
                    </div>
                {/if}
            </div>
        {/each}
        {#each messages as message}
            <div class="message">{message}</div>
        {/each}
    </div>
    <div class="input-container">
        <input
            type="text"
            bind:value={input}
            on:keydown={(e) => e.key === "Enter" && handleInput()}
            placeholder="Type a message..."
        />
        <button on:click={handleInput}>Send</button>
    </div>
</div>

<style>
    h1 {
        color: #ff3e00;
    }
    /* Dark theme styles */
    :global(body) {
        background-color: #121212;
        color: #ffffff;
        margin: 0;
        font-family: sans-serif;
    }
    .chat-container {
        display: flex;
        flex-direction: column;
        height: 100%;
    }
    .messages {
        flex: 1;
        padding: 10px;
        overflow-y: auto;
    }
    .message {
        margin-bottom: 10px;
    }
    .input-container {
        display: flex;
        padding: 10px;
        background-color: #1e1e1e;
    }
    .input-container input {
        flex: 1;
        padding: 10px;
        background-color: #2e2e2e;
        border: none;
        color: #ffffff;
    }
    .input-container button {
        margin-left: 10px;
        padding: 10px;
        background-color: #3e3e3e;
        border: none;
        color: #ffffff;
        cursor: pointer;
    }
    /* pre {
        background-color: #2e2e2e;
        padding: 10px;
        border-radius: 5px;
        color: #ffffff;
    } */
    :root {
        --dark-bg: #1a1a1a;
        --dark-card: #2d2d2d;
        --dark-text: #e1e1e1;
        --dark-meta: #8a8a8a;
        --dark-hover: #353535;
        --dark-hash: #61afef;
        --dark-level-bg: #2c3c4c;
    }

    .note-card {
        background: var(--dark-card);
        border-radius: 8px;
        padding: 16px;
        margin: 12px 0;
        box-shadow: 0 2px 4px rgba(0, 0, 0, 0.3);
        transition: all 0.2s ease;
    }

    .note-card:hover {
        transform: translateY(-2px);
        background: var(--dark-hover);
        box-shadow: 0 4px 8px rgba(0, 0, 0, 0.4);
    }

    .note-content {
        color: var(--dark-text);
        font-size: 1.1em;
        line-height: 1.5;
        white-space: pre-wrap;
        word-break: break-word;
    }

    .note-meta {
        margin-top: 12px;
        font-size: 0.9em;
        color: var(--dark-meta);
        display: flex;
        gap: 8px;
        align-items: center;
    }

    .hash {
        color: var(--dark-hash);
        font-family: monospace;
        padding: 2px 6px;
        border-radius: 4px;
        background: rgba(97, 175, 239, 0.1);
    }

    .level {
        background: var(--dark-level-bg);
        padding: 2px 6px;
        border-radius: 4px;
        font-size: 0.8em;
        color: var(--dark-text);
    }
</style>
