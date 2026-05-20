export function Footer() {
  return (
    <footer className="footer">
      <div className="container footer__inner">
        <div className="footer__copy">
          © {new Date().getFullYear()} 视频站
        </div>
      </div>
    </footer>
  );
}
