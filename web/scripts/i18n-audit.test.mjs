import { execSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { describe, it, expect } from 'vitest';

const __dirname = dirname(fileURLToPath(import.meta.url));
const webRoot = join(__dirname, '..');

describe('i18n locale audit', () => {
  it('en and ru locales are complete and RU plurals cover _many', () => {
    let exitCode = 0;
    let output = '';
    try {
      output = execSync('node scripts/i18n-audit.mjs', {
        stdio: 'pipe',
        cwd: webRoot,
      }).toString();
    } catch (e) {
      exitCode = e.status ?? 1;
      output = (e.stdout?.toString() ?? '') + (e.stderr?.toString() ?? '');
    }
    if (exitCode !== 0) console.error(output);
    expect(exitCode).toBe(0);
  });
});
