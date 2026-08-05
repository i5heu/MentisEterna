module.exports = {
    presets: [
        [
            "@babel/preset-env",
            {
                targets: { node: "current" },
            },
        ],
    ],
    plugins: [
        // Jest-only: this config is only consumed by babel-jest (the Vite
        // production build uses esbuild and never reads it). Babel's CJS
        // transform does not rewrite `import.meta`, which makes any module
        // that references it (e.g. src/api.js -> `new URL(..., import.meta.url)`)
        // fail to load under Jest as a syntax error. Rewrite `import.meta`
        // to a CJS-valid object so `import.meta.url` resolves to this file's
        // `file://` URL, matching the browser semantics closely enough for tests.
        function transformImportMeta() {
            return {
                visitor: {
                    MetaProperty(path) {
                        if (
                            path.node.meta.name === "import" &&
                            path.node.property.name === "meta"
                        ) {
                            path.replaceWithSourceString(
                                '({ url: require("url").pathToFileURL(__filename).href })',
                            );
                        }
                    },
                },
            };
        },
    ],
};
