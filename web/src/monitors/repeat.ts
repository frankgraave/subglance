export const REPEAT_ERROR = "The first repeat must come between 1 min and 24 h after the alert, in whole seconds. The 1-minute floor keeps reminders from becoming too frequent; choose Do not repeat to turn them off.";
export function validRepeat(value: string): boolean {
  return value.trim() !== "" && Number.isInteger(Number(value)) &&
    (Number(value) === 0 || (Number(value) >= 60 && Number(value) <= 86400));
}
