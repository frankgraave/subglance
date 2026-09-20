export const REPEAT_ERROR = "Repeat alert base must be a whole number from 60 to 86400 seconds. The 60-second floor prevents overly frequent reminders; choose Do not repeat to turn them off.";
export function validRepeat(value: string): boolean {
  return value.trim() !== "" && Number.isInteger(Number(value)) &&
    (Number(value) === 0 || (Number(value) >= 60 && Number(value) <= 86400));
}
