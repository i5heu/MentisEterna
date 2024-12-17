<script lang="ts">
    import { onMount } from "svelte";
    let name: string = "World";
    let messages: string[] = [];
    let input: string = "";

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
            const text = await response.text();
            messages.push(`Notes:\n${text}`);
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
        LOL
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
</style>
