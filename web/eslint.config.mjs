// Lint rules (POL-8.1): TypeScript recommended + Prettier-compatible +
// project rules. `no-alert` bans window.confirm/prompt/alert in new code
// (the legacy call sites carry targeted disables pointing at POL-8.2, which
// migrates them to the in-app dialog system). `max-len` caps line length at
// 120; Prettier (printWidth 100) keeps formatted code well under it, so a
// max-len hit means a hand-breakable line or an unbreakable literal worth a
// second look.
import tseslint from "typescript-eslint";
import prettier from "eslint-config-prettier";

export default tseslint.config(
  {
    ignores: [
      "dist/",
      "coverage/",
      "node_modules/",
      "test-results/",
      "playwright-report/",
      "playwright/.cache/",
    ],
  },
  ...tseslint.configs.recommended,
  prettier,
  {
    rules: {
      "no-alert": "error",
      // `any` pervades the dynamic settings JSON today; typed migration is
      // POL-8.8 code-health scope, so it stays allowed until then.
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/no-unused-vars": ["error", { ignoreRestSiblings: true }],
      "max-len": [
        "error",
        {
          code: 120,
          tabWidth: 2,
          ignoreUrls: true,
          ignoreStrings: false,
          ignoreTemplateLiterals: false,
          ignoreRegExpLiterals: true,
          ignoreComments: false,
        },
      ],
    },
  },
);
