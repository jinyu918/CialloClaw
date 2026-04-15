import { readFile, stat } from "node:fs/promises";
import { spawn } from "node:child_process";
import { stdin as input, stdout as output, stderr as errorOutput } from "node:process";
import path from "node:path";

const manifest = {
  worker_name: "ocr_worker",
  transport: ["stdio", "jsonrpc"],
  capabilities: ["ocr_image", "ocr_pdf", "extract_text"],
};

function readAllStdin() {
  return new Promise((resolve, reject) => {
    let data = "";
    input.setEncoding("utf8");
    input.on("data", (chunk) => {
      data += chunk;
    });
    input.on("end", () => resolve(data));
    input.on("error", reject);
  });
}

function normalizeText(value) {
  return value.replace(/<script[\s\S]*?<\/script>/gi, " ")
    .replace(/<style[\s\S]*?<\/style>/gi, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function execFile(command, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { stdio: ["ignore", "pipe", "pipe"] });
    let stdoutText = "";
    let stderrText = "";
    child.stdout.on("data", (chunk) => { stdoutText += chunk.toString(); });
    child.stderr.on("data", (chunk) => { stderrText += chunk.toString(); });
    child.on("error", reject);
    child.on("close", (code) => {
      if (code === 0) {
        resolve({ stdout: stdoutText, stderr: stderrText });
        return;
      }
      reject(new Error(stderrText.trim() || `${command} exited with code ${code}`));
    });
  });
}

async function assertPathReadable(targetPath) {
  if (typeof targetPath !== "string" || targetPath.trim() === "") {
    throw new Error("path_required");
  }
  await stat(targetPath);
}

async function extractPlainText(targetPath) {
  const extension = path.extname(targetPath).toLowerCase();
  const text = await readFile(targetPath, "utf8");
  if (extension === ".html" || extension === ".htm") {
    return normalizeText(text);
  }
  return text.trim();
}

async function extractPDFText(targetPath) {
  const result = await execFile("pdftotext", ["-layout", targetPath, "-"]);
  return normalizeText(result.stdout);
}

async function runTesseract(targetPath, language) {
  const args = [targetPath, "stdout"];
  if (typeof language === "string" && language.trim() !== "") {
    args.push("-l", language.trim());
  }
  const result = await execFile("tesseract", args);
  return normalizeText(result.stdout);
}

async function handleRequest(request) {
  switch (request.action) {
    case "health":
      return {
        ok: true,
        result: {
          status: "ok",
          worker_name: manifest.worker_name,
          capabilities: manifest.capabilities,
        },
      };
    case "extract_text": {
      await assertPathReadable(request.path);
      const text = await extractPlainText(request.path);
      return {
        ok: true,
        result: {
          path: request.path,
          text,
          language: "plain_text",
          page_count: 1,
          source: "ocr_worker_text",
        },
      };
    }
    case "ocr_image": {
      await assertPathReadable(request.path);
      const text = await runTesseract(request.path, request.language);
      return {
        ok: true,
        result: {
          path: request.path,
          text,
          language: request.language || "eng",
          page_count: 1,
          source: "ocr_worker_tesseract",
        },
      };
    }
    case "ocr_pdf": {
      await assertPathReadable(request.path);
      const text = await extractPDFText(request.path);
      return {
        ok: true,
        result: {
          path: request.path,
          text,
          language: request.language || "pdf_text",
          page_count: 1,
          source: "ocr_worker_pdf",
        },
      };
    }
    default:
      return {
        ok: false,
        error: {
          code: "unsupported_action",
          message: "unsupported action",
        },
      };
  }
}

async function main() {
  const raw = await readAllStdin();
  const trimmed = raw.trim();
  if (trimmed === "" || trimmed === "--manifest") {
    output.write(`${JSON.stringify(manifest)}\n`);
    return;
  }
  const request = JSON.parse(trimmed);
  const response = await handleRequest(request);
  output.write(`${JSON.stringify(response)}\n`);
}

main().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  errorOutput.write(`${message}\n`);
  output.write(`${JSON.stringify({ ok: false, error: { code: "worker_failed", message } })}\n`);
  process.exitCode = 1;
});
