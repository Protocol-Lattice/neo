import { createClient, NeoError } from "./neo.gen.ts";

const client = createClient("http://localhost:8080/neo");

async function main() {
  const health = await client.healthcheck.query({});
  console.log("healthcheck", health);

  const existing = await client.user.getByID.query({ id: 1 });
  console.log("existing user", existing);

  const abort = new AbortController();
  const changes = readOneChange(abort);

  const created = await client.user.create.mutate({ name: "TypeScript" });
  console.log("created user", created);

  console.log("subscription event", await changes);

  try {
    await client.user.getByID.query({ id: 999 });
  } catch (error) {
    if (error instanceof NeoError) {
      console.log("not found", { code: error.code, status: error.status, message: error.message });
      return;
    }
    throw error;
  }
}

async function readOneChange(abort: AbortController) {
  try {
    for await (const event of client.user.changes.subscribe({}, { signal: abort.signal })) {
      return event;
    }
    throw new Error("subscription closed before an event arrived");
  } finally {
    abort.abort();
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
