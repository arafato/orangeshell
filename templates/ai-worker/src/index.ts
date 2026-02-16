// orangeshell AI proxy Worker — routes requests to Workers AI.
// Deployed automatically by orangeshell's AI tab provisioning flow.

interface Env {
  AI: Ai;
  AUTH_SECRET: string;
}

interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

interface RequestBody {
  model: string;
  messages: ChatMessage[];
  stream?: boolean;
  max_tokens?: number;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    // CORS preflight
    if (request.method === "OPTIONS") {
      return new Response(null, {
        headers: {
          "Access-Control-Allow-Origin": "*",
          "Access-Control-Allow-Methods": "POST, OPTIONS",
          "Access-Control-Allow-Headers": "Content-Type, Authorization",
        },
      });
    }

    // Auth check
    const authHeader = request.headers.get("Authorization");
    if (authHeader !== `Bearer ${env.AUTH_SECRET}`) {
      return new Response("Unauthorized", { status: 401 });
    }

    if (request.method !== "POST") {
      return new Response("Method not allowed", { status: 405 });
    }

    try {
      const body = (await request.json()) as RequestBody;
      const { model, messages, stream, max_tokens } = body;

      if (!model || !messages) {
        return new Response("Missing model or messages", { status: 400 });
      }

      // Default to 4096 tokens if not specified (Workers AI default is 256, which is too low).
      const tokens = max_tokens || 4096;

      if (stream) {
        const response = await env.AI.run(model as BaseAiTextGenerationModels, {
          messages,
          stream: true,
          max_tokens: tokens,
        });
        return new Response(response as ReadableStream, {
          headers: {
            "Content-Type": "text/event-stream",
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "Access-Control-Allow-Origin": "*",
          },
        });
      }

      const result = await env.AI.run(model as BaseAiTextGenerationModels, {
        messages,
        max_tokens: tokens,
      });
      return Response.json(result, {
        headers: { "Access-Control-Allow-Origin": "*" },
      });
    } catch (err: any) {
      return Response.json(
        { error: err.message || "Internal error" },
        {
          status: 500,
          headers: { "Access-Control-Allow-Origin": "*" },
        }
      );
    }
  },
};
