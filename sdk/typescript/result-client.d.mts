import type { Client } from '@modelcontextprotocol/sdk/client/index.js';

/** Runtime environment provisioned by Controls for the assigned participant. */
export type Environment = Record<string, string | undefined> & {
  FACTORY_MCP_URL: string;
  FACTORY_MCP_TOKEN: string;
  FACTORY_THREAD_ID: string;
  FACTORY_PARTICIPANT_ID: string;
};

/** Connect an official MCP client using the invocation's scoped credentials. */
export function connect(environment: Environment, name?: string): Promise<Client>;

/** Submit a step_result and resolve only after the controller accepts it. */
export type SubmitResult = (result: Record<string, unknown>) => Promise<unknown>;

/** Discover the controller, run the callback, and close the connection. */
export function withController<T>(environment: Environment, run: (submit: SubmitResult) => Promise<T>): Promise<T>;
