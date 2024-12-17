import type { SvelteComponent } from 'svelte';
import App from './App.svelte';

const target = document.getElementById('app');

if (!target) {
    throw new Error('Could not find #app element to mount application');
}

const app = new App({
    target,
    intro: true // Enable transitions on initial render
}) as SvelteComponent;


export default app;