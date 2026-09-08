import { forwardRef, useState, type InputHTMLAttributes } from "react";
import styles from "./PasswordInput.module.css";
import { EyeIcon, EyeOffIcon } from "./icons";

/** A password <input> with a show/hide toggle — forwards its ref so
 * react-hook-form's register() still attaches directly to the real
 * input element (this only wraps it in a positioning <div>, doesn't
 * replace it), used exactly like a plain <input type="password">
 * everywhere else already does: `<PasswordInput {...register("password")} />`. */
export const PasswordInput = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function PasswordInput(props, ref) {
  const [visible, setVisible] = useState(false);
  return (
    <div className={styles.wrap}>
      <input {...props} ref={ref} type={visible ? "text" : "password"} />
      <button
        type="button"
        className={styles.toggle}
        onClick={() => setVisible((v) => !v)}
        aria-label={visible ? "Hide password" : "Show password"}
      >
        {visible ? <EyeOffIcon /> : <EyeIcon />}
      </button>
    </div>
  );
});
