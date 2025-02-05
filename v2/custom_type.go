package go_ora

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/nanitor/go-ora/v2/converters"
)

type customType struct {
	attribs  []ParameterInfo
	typ      reflect.Type
	filedMap map[string]int
}

func (conn *Connection) RegisterType(owner, typeName string, typeObj interface{}) error {
	if typeObj == nil {
		return errors.New("type object cannot be nil")
	}
	typ := reflect.TypeOf(typeObj)
	switch typ.Kind() {
	case reflect.Ptr:
		return errors.New("unsupported type object: Ptr")
	case reflect.Array:
		return errors.New("unsupported type object: Array")
	case reflect.Chan:
		return errors.New("unsupported type object: Chan")
	case reflect.Map:
		return errors.New("unsupported type object: Map")
	case reflect.Slice:
		return errors.New("unsupported type object: Slice")
	}
	if typ.Kind() != reflect.Struct {
		return errors.New("type object should be of structure type")
	}
	cust := customType{typ: typ, filedMap: map[string]int{}}
	sqlText := `SELECT ATTR_NAME, ATTR_TYPE_NAME, LENGTH, ATTR_NO 
FROM ALL_TYPE_ATTRS WHERE UPPER(OWNER)=:1 AND UPPER(TYPE_NAME)=:2`
	stmt := NewStmt(sqlText, conn)
	defer func(stmt *Stmt) {
		_ = stmt.Close()
	}(stmt)
	stmt.AddParam("1", strings.ToUpper(owner), 40, Input)
	stmt.AddParam("2", strings.ToUpper(typeName), 40, Input)
	values := make([]driver.Value, 4)
	rows, err := stmt.Query(nil)
	if err != nil {
		return err
	}
	var (
		attName     string
		attOrder    int64
		attTypeName string
		length      int64
		ok          bool
	)
	for {
		err = rows.Next(values)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if attName, ok = values[0].(string); !ok {
			return errors.New(fmt.Sprint("error reading attribute properties for type: ", typeName))
		}
		if attTypeName, ok = values[1].(string); !ok {
			return errors.New(fmt.Sprint("error reading attribute properties for type: ", typeName))
		}
		if values[2] == nil {
			length = 0
		} else {
			if length, ok = values[2].(int64); !ok {
				return errors.New(fmt.Sprint("error reading attribute properties for type: ", typeName))
			}
		}
		if attOrder, ok = values[3].(int64); !ok {
			return errors.New(fmt.Sprint("error reading attribute properties for type: ", typeName))
		}
		for int(attOrder) > len(cust.attribs) {
			cust.attribs = append(cust.attribs, ParameterInfo{
				Direction:   Input,
				Flag:        3,
				CharsetID:   conn.tcpNego.ServerCharset,
				CharsetForm: 1,
			})
		}
		param := &cust.attribs[attOrder-1]
		param.Name = attName
		param.TypeName = attTypeName
		switch strings.ToUpper(attTypeName) {
		case "NUMBER":
			param.DataType = NUMBER
			param.ContFlag = 0
			param.MaxCharLen = 0
			param.MaxLen = 22
			param.CharsetForm = 0
		case "VARCHAR2":
			param.DataType = NCHAR
			param.CharsetForm = 1
			param.ContFlag = 16
			param.MaxCharLen = int(length)
			param.MaxLen = int(length) * converters.MaxBytePerChar(param.CharsetID)
		case "NVARCHAR2":
			param.DataType = NCHAR
			param.CharsetForm = 2
			param.ContFlag = 16
			param.MaxCharLen = int(length)
			param.MaxLen = int(length) * converters.MaxBytePerChar(param.CharsetID)
		case "TIMESTAMP":
			fallthrough
		case "DATE":
			param.DataType = DATE
			param.ContFlag = 0
			param.MaxLen = 11
			param.MaxCharLen = 11
		case "RAW":
			param.DataType = RAW
			param.ContFlag = 0
			param.MaxLen = int(length)
			param.MaxCharLen = 0
			param.CharsetForm = 0
		default:
			return errors.New(fmt.Sprint("unsupported attribute type: ", attTypeName))
		}
	}
	if len(cust.attribs) == 0 {
		return errors.New(fmt.Sprint("unknown or empty type: ", typeName))
	}
	cust.loadFieldMap()
	conn.cusTyp[strings.ToUpper(typeName)] = cust
	return nil
}
func (conn *Connection) RegisterType2(typeName string, typeObj interface{}) error {
	if typeObj == nil {
		return errors.New("type object cannot be nil")
	}
	typ := reflect.TypeOf(typeObj)
	switch typ.Kind() {
	case reflect.Ptr:
		return errors.New("unsupported type object: Ptr")
	case reflect.Array:
		return errors.New("unsupported type object: Array")
	case reflect.Chan:
		return errors.New("unsupported type object: Chan")
	case reflect.Map:
		return errors.New("unsupported type object: Map")
	case reflect.Slice:
		return errors.New("unsupported type object: Slice")
	}
	if typ.Kind() != reflect.Struct {
		return errors.New("type object should be of structure type")
	}
	cust := customType{typ: typ, filedMap: map[string]int{}}
	sqlText := `
DECLARE
    toid raw(128);
    vers number;
    tds long raw;
    instantiable varchar(100);
    supertype_owner varchar(100);
    supertype_name varchar(100);
    attr_rc sys_refcursor;
    subtype_rc sys_refcursor;
    retVal number;
BEGIN
	:retVal := dbms_pickler.get_type_shape(:typeName, toid, vers, tds, 
        instantiable, supertype_owner, supertype_name, :att_rc, subtype_rc);
END;`
	stmt := NewStmt(sqlText, conn)
	defer func(stmt *Stmt) {
		_ = stmt.Close()
	}(stmt)
	stmt.AddParam("retVal", 0, 8, Output)
	stmt.AddParam("typeName", typeName, 40, Input)
	stmt.AddRefCursorParam("att_rc")
	_, err := stmt.Exec(nil)
	if err != nil {
		return err
	}
	if stmt.Pars[0].Value.(int64) != 0 {
		return errors.New(fmt.Sprint("unknown type: ", typeName))
	}
	if cursor, ok := stmt.Pars[2].Value.(RefCursor); ok {
		defer func(cursor *RefCursor) {
			_ = cursor.Close()
		}(&cursor)
		rows, err := cursor.Query()
		if err != nil {
			return err
		}

		var (
			attName     string
			attOrder    int64
			attTypeName string
		)
		values := make([]driver.Value, 10)
		for {
			err = rows.Next(values)
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return err
			}
			// check for error and if == io.EOF break

			if attName, ok = values[1].(string); !ok {
				return errors.New(fmt.Sprint("error reading attribute properties for type: ", typeName))
			}
			if attOrder, ok = values[2].(int64); !ok {
				return errors.New(fmt.Sprint("error reading attribute properties for type: ", typeName))
			}
			if attTypeName, ok = values[3].(string); !ok {
				return errors.New(fmt.Sprint("error reading attribute properties for type: ", typeName))
			}
			//fmt.Println(attOrder, "\t", attName, "\t", attTypeName)
			for int(attOrder) > len(cust.attribs) {
				cust.attribs = append(cust.attribs, ParameterInfo{
					Direction:   Input,
					Flag:        3,
					CharsetID:   conn.tcpNego.ServerCharset,
					CharsetForm: 1,
				})
			}
			param := &cust.attribs[attOrder-1]
			param.Name = attName
			param.TypeName = attTypeName
			switch strings.ToUpper(attTypeName) {
			case "NUMBER":
				param.DataType = NUMBER
				param.ContFlag = 0
				param.MaxCharLen = 0
				param.MaxLen = 22
				param.CharsetForm = 0
			case "VARCHAR2":
				param.DataType = NCHAR
				param.CharsetForm = 1
				param.ContFlag = 16
				param.MaxCharLen = 1000
				param.MaxLen = 1000 * converters.MaxBytePerChar(param.CharsetID)
			case "NVARCHAR2":
				param.DataType = NCHAR
				param.CharsetForm = 2
				param.ContFlag = 16
				param.MaxCharLen = 1000
				param.MaxLen = 1000 * converters.MaxBytePerChar(param.CharsetID)
			case "TIMESTAMP":
				fallthrough
			case "DATE":
				param.DataType = DATE
				param.ContFlag = 0
				param.MaxLen = 11
				param.MaxCharLen = 11
			case "RAW":
				param.DataType = RAW
				param.ContFlag = 0
				param.MaxLen = 1000
				param.MaxCharLen = 0
				param.CharsetForm = 0
			default:
				return errors.New(fmt.Sprint("unsupported attribute type: ", attTypeName))
			}
		}
	}
	cust.loadFieldMap()
	conn.cusTyp[strings.ToUpper(typeName)] = cust
	return nil
}
func (cust *customType) loadFieldMap() {
	typ := cust.typ
	for x := 0; x < typ.NumField(); x++ {
		f := typ.Field(x)
		tag := f.Tag.Get("oracle")
		if len(tag) == 0 {
			continue
		}
		tag = strings.Trim(tag, "\"")
		parts := strings.Split(tag, ",")
		for _, part := range parts {
			subs := strings.Split(part, ":")
			if len(subs) == 0 {
				continue
			}
			if strings.TrimSpace(strings.ToLower(subs[0])) == "name" {
				if len(subs) == 1 {
					continue
				}
				fieldID := strings.TrimSpace(strings.ToUpper(subs[1]))
				cust.filedMap[fieldID] = x
			}
		}
	}
}
func (cust *customType) getObject() interface{} {
	typ := cust.typ
	obj := reflect.New(typ)
	for _, attrib := range cust.attribs {
		if fieldIndex, ok := cust.filedMap[attrib.Name]; ok {
			obj.Elem().Field(fieldIndex).Set(reflect.ValueOf(attrib.Value))
		}
	}
	return obj.Elem().Interface()
}
