module.exports = {
    testEnvironment: "node",
    // Support the ESM source files: compile them through Babel to CJS.
    transform: {
        "^.+\\.js$": ["babel-jest", { configFile: "./babel.config.cjs" }],
    },
    transformIgnorePatterns: [
        // Allow transform of CJS/ESM deps that ship untranspiled ESM if needed.
        "node_modules/(?!(vue)/)",
    ],
    moduleFileExtensions: ["js", "mjs", "cjs"],
    testMatch: ["**/__tests__/**/*.test.js", "**/*.test.js"],
    clearMocks: true,
};
