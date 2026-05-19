import fs from "node:fs/promises";
import path from "node:path";

export async function writeFileAtomic(filePath: string, content: string | Uint8Array) {
  await fs.mkdir(path.dirname(filePath), { recursive: true });
  const tmpPath = `${filePath}.tmp`;
  await fs.writeFile(tmpPath, content);
  await fs.rename(tmpPath, filePath);
}
