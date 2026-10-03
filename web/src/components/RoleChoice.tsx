/**
 * The one control for choosing a role: Viewer, Editor or Admin.
 *
 * Accounts and API tokens took the same three roles with two different
 * controls, a native select on Users and a segmented control on API tokens.
 * Three mutually exclusive options are the segmented control's case
 * (DESIGN.md §7.2): every role stays visible, so comparing them costs no click.
 * This wrapper is the only place a role choice is drawn, and RoleChoice.test.tsx
 * fails on a role picker built anywhere else.
 *
 * The control only chooses. Saving is the caller's: a form sends the role with
 * everything else, and a list row saves it from its own button, so a stray
 * press on the next segment is a draft rather than a change to what someone
 * may do.
 */
import { SegmentedControl } from "./SegmentedControl";
import { ROLES, roleLabel, type Role } from "../auth/roles";

export type RoleChoiceProps = {
  /** The group's accessible name: "Role" in a form, "Role for <who>" in a row. */
  label: string;
  value: Role;
  onChange: (next: Role) => void;
  /** The roles on offer, least privilege first. A token never gets more than its owner. */
  roles?: readonly Role[];
  describedBy?: string;
  disabled?: boolean;
};

export function RoleChoice({ label, value, onChange, roles = ROLES, describedBy, disabled }: RoleChoiceProps) {
  return (
    <SegmentedControl label={label} value={value} onChange={onChange} describedBy={describedBy} disabled={disabled}
      options={roles.map((role) => ({ id: role, label: roleLabel(role) }))} />
  );
}
