// @vitest-environment jsdom
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthScreen } from "./AuthScreen";
import { ApiError, MIN_PASSWORD_LENGTH } from "./api";
import type { User } from "./api";

/**
 * The forms themselves: what they send, what they refuse to send, and where a
 * server's complaint ends up on screen.
 */

afterEach(cleanup);

const user: User = {
  id: 1,
  email: "me@example.com",
  role: "admin",
  created_at: "",
};

function fill(label: RegExp, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

function api(over: Partial<{ login: unknown; createFirstUser: unknown }> = {}) {
  return {
    login: vi.fn(() => Promise.resolve(user)),
    createFirstUser: vi.fn(() => Promise.resolve(user)),
    ...over,
  } as never;
}

describe("signing in", () => {
  it("sends the credentials and hands back the user", async () => {
    const onSignedIn = vi.fn();
    const stub = api();
    render(<AuthScreen mode="login" onSignedIn={onSignedIn} api={stub} />);

    fill(/Email/, "me@example.com");
    fill(/Password/, "correct horse battery staple");
    fireEvent.click(screen.getByRole("button", { name: /Sign in/ }));

    await waitFor(() => expect(onSignedIn).toHaveBeenCalledWith(user));
    expect(
      (stub as unknown as { login: ReturnType<typeof vi.fn> }).login,
    ).toHaveBeenCalledWith("me@example.com", "correct horse battery staple");
  });

  it("does not enforce the length rule on an existing account", () => {
    render(<AuthScreen mode="login" onSignedIn={vi.fn()} api={api()} />);

    fill(/Email/, "me@example.com");
    fill(/Password/, "short");

    /*
     * An account created before the minimum was raised must still be able to
     * log in. A client that refuses to send the password would lock that
     * person out of their own instance over a rule they never agreed to, and
     * the server is the only party that knows what it will accept.
     */
    expect(
      (screen.getByRole("button", { name: /Sign in/ }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
  });

  it("puts a field-tagged rejection on the input it blames", async () => {
    const stub = api({
      login: vi.fn(() =>
        Promise.reject(
          new ApiError(400, "a valid email address is required", null, "email"),
        ),
      ),
    });
    render(<AuthScreen mode="login" onSignedIn={vi.fn()} api={stub} />);

    fill(/Email/, "not-an-address");
    fill(/Password/, "correct horse battery staple");
    fireEvent.click(screen.getByRole("button", { name: /Sign in/ }));

    const message = await screen.findByRole("alert");
    expect(message.textContent).toContain("valid email address");
    const email = screen.getByLabelText(/Email/);
    expect(email.getAttribute("aria-invalid")).toBe("true");
    // Tied on, not merely nearby: the association is what a screen reader
    // uses to read the complaint when focus lands on the field.
    expect(email.getAttribute("aria-describedby")).toBe(message.id);
  });

  it("turns a rate limit into a wait the user can act on", async () => {
    const stub = api({
      login: vi.fn(() =>
        Promise.reject(
          new ApiError(429, "too many failed attempts, try again later", 900),
        ),
      ),
    });
    render(<AuthScreen mode="login" onSignedIn={vi.fn()} api={stub} />);

    fill(/Email/, "me@example.com");
    fill(/Password/, "wrong password here");
    fireEvent.click(screen.getByRole("button", { name: /Sign in/ }));

    expect((await screen.findByRole("alert")).textContent).toContain(
      "15 minutes",
    );
  });

  it("says the server is unreachable rather than blaming the password", async () => {
    const stub = api({
      login: vi.fn(() => Promise.reject(new TypeError("Failed to fetch"))),
    });
    render(<AuthScreen mode="login" onSignedIn={vi.fn()} api={stub} />);

    fill(/Email/, "me@example.com");
    fill(/Password/, "correct horse battery staple");
    fireEvent.click(screen.getByRole("button", { name: /Sign in/ }));

    expect((await screen.findByRole("alert")).textContent).toContain(
      "Could not reach the server",
    );
  });
});

describe("first-run setup", () => {
  it("will not submit a password the server would reject", () => {
    render(<AuthScreen mode="setup" onSignedIn={vi.fn()} api={api()} />);

    fill(/Email/, "me@example.com");
    fill(/Password/, "a".repeat(MIN_PASSWORD_LENGTH - 1));
    expect(
      (
        screen.getByRole("button", {
          name: /Create account/,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);

    fill(/Password/, "a".repeat(MIN_PASSWORD_LENGTH));
    expect(
      (
        screen.getByRole("button", {
          name: /Create account/,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });

  it("shows how far off the password is while it is being typed", () => {
    render(<AuthScreen mode="setup" onSignedIn={vi.fn()} api={api()} />);

    fill(/Password/, "a".repeat(MIN_PASSWORD_LENGTH - 2));
    expect(screen.getByText(/2 more characters/)).toBeTruthy();
  });

  it("creates the account and signs in, without a second login step", async () => {
    const onSignedIn = vi.fn();
    const stub = api();
    render(<AuthScreen mode="setup" onSignedIn={onSignedIn} api={stub} />);

    fill(/Email/, "me@example.com");
    fill(/Password/, "correct horse battery staple");
    fireEvent.click(screen.getByRole("button", { name: /Create account/ }));

    // The API signs the new admin in as part of creating them; asking for the
    // password a second time would be the product distrusting its own reply.
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledWith(user));
  });

  it("tells a second tab to reload when setup was already completed elsewhere", async () => {
    const stub = api({
      createFirstUser: vi.fn(() =>
        Promise.reject(new ApiError(409, "setup has already been completed")),
      ),
    });
    render(<AuthScreen mode="setup" onSignedIn={vi.fn()} api={stub} />);

    fill(/Email/, "me@example.com");
    fill(/Password/, "correct horse battery staple");
    fireEvent.click(screen.getByRole("button", { name: /Create account/ }));

    // The next action is a reload, not a retry: this form is now permanently
    // closed and pressing the button again can only fail the same way.
    expect((await screen.findByRole("alert")).textContent).toContain(
      "Reload the page",
    );
  });

  it("asks the password manager to store a new password, not to fill an old one", () => {
    render(<AuthScreen mode="setup" onSignedIn={vi.fn()} api={api()} />);
    expect(screen.getByLabelText(/Password/).getAttribute("autocomplete")).toBe(
      "new-password",
    );

    cleanup();
    render(<AuthScreen mode="login" onSignedIn={vi.fn()} api={api()} />);
    expect(screen.getByLabelText(/Password/).getAttribute("autocomplete")).toBe(
      "current-password",
    );
  });
});
