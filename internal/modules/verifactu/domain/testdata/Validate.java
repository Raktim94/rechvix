import javax.xml.XMLConstants;
import javax.xml.transform.stream.StreamSource;
import javax.xml.validation.*;
import java.io.File;
public class Validate {
  public static void main(String[] a) throws Exception {
    SchemaFactory f = SchemaFactory.newInstance(XMLConstants.W3C_XML_SCHEMA_NS_URI);
    f.setProperty(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "file");
    Schema s = f.newSchema(new File(a[0]));
    Validator v = s.newValidator();
    try { v.validate(new StreamSource(new File(a[1]))); System.out.println("VALID " + a[1]); }
    catch (Exception e) { System.out.println("INVALID " + a[1] + ": " + e.getMessage()); System.exit(1); }
  }
}
